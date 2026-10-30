// Package ai implements the competition MVP's AI triage: one short
// report text (typed or transcribed from voice) becomes a structured,
// bilingual incident summary that drives dispatch. The LLM provider is
// optional and config-gated; when it is missing, slow, or returns
// unusable output, the deterministic rule classifier below is used
// instead, so an SOS is never blocked waiting on AI. Precise
// coordinates are never sent to the provider: only the report text
// and an area label (for example "Canggu, Bali") leave this service.
package ai

import (
	"context"
	"strings"
)

// Categories are the AI triage labels. Each maps to one of the
// existing Alert emergency types (DANGER / MEDICAL / ACCIDENT).
const (
	CategoryAccidentTraffic = "ACCIDENT_TRAFFIC"
	CategoryMedical         = "MEDICAL"
	CategoryDrowning        = "DROWNING"
	CategoryFire            = "FIRE"
	CategoryDanger          = "DANGER"
	CategoryNaturalDisaster = "NATURAL_DISASTER"
	CategoryOther           = "OTHER"
)

// EmergencyTypeForCategory maps an AI category to the stored Alert
// emergency type used by dispatch and push templates.
func EmergencyTypeForCategory(category string) string {
	switch category {
	case CategoryAccidentTraffic:
		return "ACCIDENT"
	case CategoryMedical, CategoryDrowning:
		return "MEDICAL"
	default:
		return "DANGER"
	}
}

// ValidCategory reports whether a category is one of the known labels.
func ValidCategory(category string) bool {
	switch category {
	case CategoryAccidentTraffic, CategoryMedical, CategoryDrowning,
		CategoryFire, CategoryDanger, CategoryNaturalDisaster, CategoryOther:
		return true
	default:
		return false
	}
}

// Input is everything the triage needs. AreaLabel is a coarse area
// name only; precise latitude/longitude must not be included.
type Input struct {
	Text      string
	AreaLabel string
}

// Result is one triage outcome. Source is "llm", "rules", or
// "override" (the user corrected the category before confirming).
// NeedsClarification means the report was too vague to classify
// confidently; severity is escalated in that case (see Validate).
type Result struct {
	Category           string
	EmergencyType      string
	Severity           int // 1 (low) .. 5 (critical)
	Confidence         float64
	Language           string
	SummaryID          string
	SummaryEN          string
	Keywords           []string
	Needs              []string
	NeedsClarification bool
	Source             string
}

// Validate clamps a result into the contract the rest of the system
// relies on: known category, severity 1..5, non-empty summaries, and
// escalation when confidence is low. Uncertain reports are treated
// as serious rather than downgraded, because a missed critical
// incident costs more than an over-notified minor one.
func Validate(r Result) Result {
	if !ValidCategory(r.Category) {
		r.Category = CategoryOther
	}
	r.EmergencyType = EmergencyTypeForCategory(r.Category)
	if r.Severity < 1 {
		r.Severity = 1
	}
	if r.Severity > 5 {
		r.Severity = 5
	}
	if r.Confidence < 0 {
		r.Confidence = 0
	}
	if r.Confidence > 1 {
		r.Confidence = 1
	}
	if r.Confidence <= 0.5 && r.Severity < 4 {
		r.Severity = 4
		r.NeedsClarification = true
	}
	if strings.TrimSpace(r.SummaryID) == "" {
		r.SummaryID = categoryLabelID(r.Category)
	}
	if strings.TrimSpace(r.SummaryEN) == "" {
		r.SummaryEN = categoryLabelEN(r.Category)
	}
	r.SummaryID = truncate(r.SummaryID, 500)
	r.SummaryEN = truncate(r.SummaryEN, 500)
	return r
}

func categoryLabelID(category string) string {
	switch category {
	case CategoryAccidentTraffic:
		return "Kecelakaan lalu lintas"
	case CategoryMedical:
		return "Darurat medis"
	case CategoryDrowning:
		return "Orang tenggelam / bahaya di air"
	case CategoryFire:
		return "Kebakaran"
	case CategoryDanger:
		return "Situasi berbahaya"
	case CategoryNaturalDisaster:
		return "Bencana alam"
	default:
		return "Darurat (belum terklasifikasi)"
	}
}

func categoryLabelEN(category string) string {
	switch category {
	case CategoryAccidentTraffic:
		return "Traffic accident"
	case CategoryMedical:
		return "Medical emergency"
	case CategoryDrowning:
		return "Drowning / water danger"
	case CategoryFire:
		return "Fire"
	case CategoryDanger:
		return "Dangerous situation"
	case CategoryNaturalDisaster:
		return "Natural disaster"
	default:
		return "Emergency (unclassified)"
	}
}

// Triager turns report text into a Result. Implementations must be
// fail-open: an error must not prevent the SOS from being created;
// callers fall back to dispatch without triage when Triage errors.
type Triager interface {
	Triage(ctx context.Context, in Input) (Result, error)
}

// Classifier is the deterministic fallback: keyword scoring over
// Indonesian and English report text. It never calls the network,
// never hallucinates details, and always echoes the reporter's own
// words in the summaries so responders see what was actually said.
type Classifier struct{}

// Classify scores the text and returns a rule-based Result.
func (Classifier) Classify(text string) Result {
	lowered := strings.ToLower(text)
	matched := map[string][]string{}
	scores := map[string]int{}
	for _, rule := range categoryRules {
		for _, kw := range rule.keywords {
			if strings.Contains(lowered, kw.word) {
				scores[rule.category] += kw.weight
				matched[rule.category] = append(matched[rule.category], kw.word)
			}
		}
	}
	category := CategoryOther
	best := 0
	for _, rule := range categoryRules {
		if scores[rule.category] > best {
			best = scores[rule.category]
			category = rule.category
		}
	}
	severity := baseSeverity(category)
	for _, kw := range criticalKeywords {
		if strings.Contains(lowered, kw) {
			severity = 5
			break
		}
	}
	if severity < 4 {
		for _, kw := range seriousKeywords {
			if strings.Contains(lowered, kw) {
				severity = 4
				break
			}
		}
	}
	var confidence float64
	switch {
	case best >= 4:
		confidence = 0.85
	case best >= 2:
		confidence = 0.65
	case best == 1:
		confidence = 0.5
	default:
		confidence = 0.35
	}
	quote := truncate(strings.TrimSpace(text), 140)
	summaryID := categoryLabelID(category)
	summaryEN := categoryLabelEN(category)
	if quote != "" {
		summaryID += ": " + quote
		summaryEN += ": " + quote
	}
	keywords := dedup(matched[category])
	if len(keywords) > 5 {
		keywords = keywords[:5]
	}
	r := Result{
		Category:           category,
		Severity:           severity,
		Confidence:         confidence,
		Language:           detectLanguage(lowered),
		SummaryID:          summaryID,
		SummaryEN:          summaryEN,
		Keywords:           keywords,
		Needs:              needsFor(category),
		NeedsClarification: confidence < 0.5 || len(strings.Fields(text)) < 3,
		Source:             "rules",
	}
	return Validate(r)
}

// NeedsForCategory returns the short responder-need tags for a category.
func NeedsForCategory(category string) []string {
	return needsFor(category)
}

func baseSeverity(category string) int {
	switch category {
	case CategoryDrowning:
		return 5
	case CategoryAccidentTraffic, CategoryFire, CategoryNaturalDisaster:
		return 4
	default:
		return 3
	}
}

func needsFor(category string) []string {
	switch category {
	case CategoryAccidentTraffic:
		return []string{"first-aid", "ambulance"}
	case CategoryMedical:
		return []string{"first-aid", "ambulance"}
	case CategoryDrowning:
		return []string{"lifeguard", "first-aid", "ambulance"}
	case CategoryFire:
		return []string{"fire-service", "evacuation"}
	case CategoryNaturalDisaster:
		return []string{"evacuation", "shelter"}
	case CategoryDanger:
		return []string{"police", "first-aid"}
	default:
		return []string{"first-aid"}
	}
}

type keyword struct {
	word   string
	weight int
}

type categoryRule struct {
	category string
	keywords []keyword
}

var categoryRules = []categoryRule{
	{CategoryAccidentTraffic, []keyword{
		{"kecelakaan", 3}, {"accident", 3}, {"crash", 3}, {"tabrak", 3},
		{"motor", 2}, {"motorcycle", 2}, {"scooter", 2}, {"mobil", 1}, {"car crash", 3},
		{"jatuh dari motor", 3}, {"fell off", 2}, {"tertabrak", 3},
		{"hit by", 2}, {"ditabrak", 3},
	}},
	{CategoryDrowning, []keyword{
		{"tenggelam", 4}, {"drowning", 4}, {"drown", 3}, {"terseret ombak", 3},
		{"swept away", 3}, {"di laut", 1}, {"in the sea", 1},
	}},
	{CategoryFire, []keyword{
		{"kebakaran", 4}, {"fire", 2}, {"terbakar", 3}, {"burning", 2},
		{"api membesar", 3}, {"asap tebal", 2},
	}},
	{CategoryMedical, []keyword{
		{"pingsan", 3}, {"unconscious", 3}, {"faint", 2}, {"sesak", 3},
		{"can't breathe", 3}, {"cannot breathe", 3}, {"nyeri dada", 3}, {"chest pain", 3},
		{"sakit", 1}, {"pain", 1}, {"kejang", 3}, {"seizure", 3}, {"stroke", 3},
		{"alergi", 2}, {"allergic", 2}, {"muntah darah", 3}, {"demam tinggi", 1},
	}},
	{CategoryDanger, []keyword{
		{"begal", 3}, {"rampok", 3}, {"robbed", 3}, {"mugging", 3},
		{"pisau", 3}, {"knife", 3}, {"ancaman", 2}, {"threat", 2},
		{"dipukul", 2}, {"attacked", 2}, {"menyerang", 2}, {"pencuri", 2}, {"thief", 2},
	}},
	{CategoryNaturalDisaster, []keyword{
		{"gempa", 4}, {"earthquake", 4}, {"banjir", 3}, {"flood", 3},
		{"tsunami", 4}, {"longsor", 3}, {"landslide", 3}, {"badai", 2}, {"storm surge", 3},
	}},
}

var criticalKeywords = []string{
	"berdarah", "bleeding", "darah banyak", "tidak sadar", "unconscious",
	"tidak bisa bergerak", "can't move", "cannot move", "tenggelam", "drowning",
	"terjebak", "trapped", "sesak napas", "can't breathe", "pingsan",
}

var seriousKeywords = []string{
	"patah", "broken", "fracture", "luka", "wound", "jatuh", "fell",
	"kecelakaan", "accident", "terbakar", "kebakaran", "nyeri dada",
}

var indonesianWords = []string{"tolong", "saya", "aku", "ada", "yang", "tidak", "sakit", "kecelakaan", "tenggelam", "kebakaran", "gempa", "seseorang", "orang", "motor", "jatuh", "darah", "bantu"}
var englishWords = []string{"help", "please", "my", "friend", "fell", "bleeding", "accident", "drowning", "fire", "cannot", "can't", "someone", "scooter", "bike", "blood"}

func detectLanguage(lowered string) string {
	id, en := 0, 0
	for _, w := range indonesianWords {
		if strings.Contains(lowered, w) {
			id++
		}
	}
	for _, w := range englishWords {
		if strings.Contains(lowered, w) {
			en++
		}
	}
	switch {
	case id > en:
		return "id"
	case en > id:
		return "en"
	default:
		return "unknown"
	}
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
