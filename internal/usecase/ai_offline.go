package usecase

import (
	"context"
	"strings"
	"unicode"

	"golang-clean-architecture/internal/entity"
)

// OfflineClassifier is the deterministic, dependency-free AI triage
// fallback: a keyword classifier for Indonesian/English distress
// messages. It never errors, so the SOS trigger always has a baseline
// classification even when the LLM provider is down or unconfigured.
// An empty description yields a zero result (no signal), keeping the
// legacy trigger behaviour byte-for-byte identical.
//
// The taxonomy is deliberately fixed at DANGER/MEDICAL/ACCIDENT because
// `model.CreateSOSRequest` validates `oneof=DANGER MEDICAL ACCIDENT` and
// the frontend renders those three. Extra coverage therefore lives in
// the keyword lists (including disasters like fires/floods/earthquakes,
// which map to DANGER) rather than in new types.
type OfflineClassifier struct{}

func NewOfflineClassifier() *OfflineClassifier { return &OfflineClassifier{} }

// classifierRule groups the phrases that hint at one emergency type.
// strong = phrases that are almost always a life-safety emergency;
// weak = supporting/ambiguous hints (e.g. "motor", "luka") that add
// weight but should not outweigh a strong signal from another type.
type classifierRule struct {
	kind   string
	strong []string
	weak   []string
}

// classifierRules covers Indonesian everyday speech, common typos and
// slang (begal, jambret, modar, ambulan, nafas), regional/transit
// harassment terms, natural disasters, plus English equivalents. SOS
// descriptions are typed in a hurry, often with stretched letters
// ("tolonggg"), so matching is repeat-insensitive and tolerant of
// Indonesian/English suffixes (see tokenMatches).
var classifierRules = []classifierRule{
	{kind: entity.EmergencyTypeMedical, strong: []string{
		// Tidak sadarkan diri / pernapasan / jantung.
		"pingsan", "tidak sadar", "tak sadar", "tidak sadarkan diri", "tak sadarkan diri",
		"kehilangan kesadaran", "hilang kesadaran",
		"sesak napas", "sesak nafas", "sulit bernapas", "sulit bernafas", "sesak",
		"napas berhenti", "nafas berhenti", "henti napas", "henti nafas",
		"berhenti bernapas", "berhenti bernafas", "tidak bernapas", "tidak bernafas",
		"sesak dada", "nyeri dada", "sakit dada",
		"serangan jantung", "henti jantung", "jantung berhenti", "sakit jantung", "jantung",
		"stroke", "kejang", "epilepsi", "asma", "koma", "syok", "sekarat",
		// Perdarahan, luka, patah tulang.
		"berdarah", "pendarahan", "muntah darah", "bab berdarah", "mimisan",
		"luka berat", "luka parah", "luka bakar", "patah tulang", "tulang patah",
		"cedera kepala", "cidera kepala", "benturan kepala",
		// Keracunan, gigitan, sengatan.
		"keracunan", "racun", "overdosis", "gantung diri",
		"digigit ular", "gigitan ular", "digigit anjing", "tersengat", "sengatan",
		// Tenggelam, kehamilan, penyakit akut.
		"tenggelam", "hanyut", "melahirkan", "keguguran", "hamil",
		"dehidrasi", "demam tinggi", "panas tinggi", "demam berdarah", "dbd",
		"alergi berat", "anafilaksis", "gula darah", "diabetes",
		// English.
		"heart attack", "cardiac arrest", "seizure", "overdose", "poisoning",
		"drowning", "unconscious", "fainted", "faint", "bleeding", "hemorrhage",
		"choking", "asthma", "allergic reaction", "anaphylaxis", "fracture",
		"broken bone", "snake bite", "dog bite", "pregnant", "labor", "miscarriage",
		"chest pain", "severe pain", "can't breathe", "cannot breathe",
		"not breathing", "stopped breathing", "difficulty breathing", "bleeding heavily",
		"burn injury",
	}, weak: []string{
		"sakit", "luka", "demam", "lemas", "lelah", "mual", "muntah", "diare", "mencret",
		"batuk", "pilek", "flu", "darah", "medis", "obat",
		"ambulans", "ambulan", "rumah sakit", "puskesmas", "klinik", "dokter", "perawat",
		"infus", "oksigen", "perban", "memar", "bengkak", "benjol",
		"pusing", "sakit kepala", "nyeri", "kram", "sakit perut", "maag", "tipes",
		"gatal", "alergi", "sakit gigi", "sakit mata", "sesak perut",
		// English.
		"sick", "ill", "pain", "injury", "wound", "fever", "vomit", "diarrhea",
		"cough", "doctor", "hospital", "ambulance", "medicine", "nurse",
		"dizzy", "headache", "stomachache", "allergy", "disease", "infection",
	}},
	{kind: entity.EmergencyTypeAccident, strong: []string{
		"kecelakaan", "tabrakan", "tertabrak", "ditabrak", "menabrak", "nabrak", "tabrak lari",
		"terserempet", "menyerempet", "terguling", "terbalik", "terperosok", "masuk jurang",
		"terjatuh", "jatuh dari", "jatuh ke", "jatuh di", "terpeleset", "tersandung",
		"terbentur", "benturan", "terhantam", "tertimpa", "terjepit", "tergencet", "terlempar",
		"rem blong", "blong", "ban pecah", "pecah ban", "kecelakaan kerja",
		"kecelakaan lalu lintas", "tabrakan beruntun",
		// English.
		"accident", "collision", "crash", "car crash", "motorcycle accident",
		"hit by", "run over", "fell from", "fell down", "slipped", "tripped",
		"rolled over", "overturned", "pileup", "pile-up", "derailed", "capsized", "sank",
	}, weak: []string{
		// Kendaraan dan jalan: hanya penguat, bukan sinyal tunggal.
		"motor", "mobil", "kendaraan", "sepeda", "kereta", "bus", "truk", "kapal",
		"feri", "pesawat", "helikopter", "perahu", "ojek", "angkot", "taksi",
		"jalan raya", "jalan tol", "lalu lintas", "oleng", "selip", "licin",
		"jatuh", "terjatuh",
		// English.
		"vehicle", "car", "motorcycle", "truck", "train", "ship", "boat", "plane",
		"road", "traffic", "slippery", "fell", "fall",
	}},
	{kind: entity.EmergencyTypeDanger, strong: []string{
		// Perampokan, pencurian, penculikan.
		"begal", "dibegal", "rampok", "dirampok", "perampok", "rampasan", "penjarahan",
		"maling", "pencuri", "pencurian", "curanmor", "copet", "pencopet",
		"jambret", "dijambret", "penjambretan", "penodongan", "ditodong", "todong",
		"culik", "diculik", "penculikan", "sandera", "disandera", "penyanderaan",
		// Ancaman dan senjata.
		"diancam", "mengancam", "ancaman", "diteror", "teror", "teroris", "terorisme",
		"bom", "bom bunuh diri", "meledak", "peledak", "granat",
		"senjata", "senjata tajam", "sajam", "pistol", "senapan", "parang", "celurit",
		"golok", "pisau", "samurai", "air keras",
		"bacok", "dibacok", "tikam", "ditikam", "menusuk", "ditusuk", "penusukan",
		"ditembak", "penembakan", "penembak",
		// Kekerasan seksual dan fisik.
		"pemerkosaan", "diperkosa", "pemerkosa", "pelecehan", "dilecehkan",
		"cabul", "pencabulan", "kekerasan", "kdrt", "tawuran", "tawur",
		"bentrok", "ricuh", "huru hara", "pengeroyokan", "dikeroyok",
		"dianiaya", "penganiayaan", "dipukul", "pemukulan",
		// Pengejaran.
		"dikejar", "dibuntuti", "diikuti orang", "diburu",
		// Kebakaran, bencana, hewan berbahaya.
		"kebakaran", "terbakar", "kobaran api", "api", "asap tebal",
		"kebocoran gas", "gas bocor", "karhutla", "pembakaran", "dibakar",
		"banjir", "banjir bandang", "tanah longsor", "longsor", "tertimbun",
		"gempa", "gempa bumi", "tsunami", "gunung meletus", "erupsi", "abu vulkanik",
		"angin puting beliung", "angin topan", "badai", "pohon tumbang",
		"korsleting", "koslet", "konslet",
		"hewan buas", "anjing galak", "ular", "buaya", "macan", "harimau", "kalajengking",
		"perang",
		// English.
		"robbery", "robbed", "mugging", "mugged", "burglar", "burglary", "thief",
		"theft", "stolen", "kidnap", "kidnapped", "abducted", "hostage",
		"threat", "threatened", "terror", "terrorist", "bomb", "explosion",
		"exploded", "weapon", "gun", "rifle", "shotgun", "knife", "machete",
		"stabbed", "shot", "assault", "attacked", "rape", "raped", "rapist",
		"harassment", "abuse", "domestic violence", "riot", "rioting",
		"arson", "fire", "burning", "on fire", "flood", "earthquake", "landslide",
		"tsunami", "volcano", "eruption", "tornado", "storm", "tree fell",
		"wild animal", "dog attack", "snake", "crocodile", "tiger", "chased",
		"stalker", "stalking",
	}, weak: []string{
		"orang asing", "orang tak dikenal", "mencurigakan", "curiga",
		"diganggu", "digangguin", "dicolek", "menguntit",
		"panik", "takut", "sendirian",
		// English.
		"stranger", "suspicious", "followed", "panic", "scared", "afraid",
	}},
}

// urgentSeverityKeywords add severityBoost each, capped at severityCap.
// They must describe a *life-threatening* escalation, not just a topic
// keyword, otherwise everything drowns at severity 1.0.
var urgentSeverityKeywords = []string{
	"kritis", "darurat", "segera", "secepatnya", "buruan", "gawat", "fatal",
	"parah", "nyawa", "korban jiwa", "banyak korban", "banyak darah",
	"tidak sadar", "tidak sadarkan diri", "tak sadarkan diri",
	"henti napas", "henti nafas", "berhenti bernapas", "tidak bernapas",
	"henti jantung", "jantung berhenti", "koma", "sekarat", "kejang",
	"muntah darah", "meledak", "bom", "kebakaran", "terbakar",
	"banjir bandang", "tsunami", "gempa", "longsor", "tenggelam", "hanyut",
	"meninggal", "tewas",
	// English.
	"emergency", "critical", "urgent", "immediately", "right now", "now",
	"dead", "death", "dying", "unconscious", "not breathing",
	"stopped breathing", "cardiac arrest", "severe", "massive",
	"explosion", "exploded", "on fire", "trapped", "many victims",
	"multiple victims", "bleeding heavily",
}

// severityBase is the score of any classified message; each urgent
// keyword adds severityBoost, capped at 1.
const (
	severityBase  = 0.6
	severityBoost = 0.2
	severityCap   = 1.0

	strongWeight = 2
	weakWeight   = 1
)

// compiledKeyword keeps the original spelling for the summary while
// matching on repeat-squeezed tokens.
type compiledKeyword struct {
	display string
	tokens  []string
}

type compiledRule struct {
	kind   string
	strong []compiledKeyword
	weak   []compiledKeyword
}

var (
	compiledRules  = compileRules(classifierRules)
	compiledUrgent = compileKeywords(urgentSeverityKeywords)
)

func compileRules(rules []classifierRule) []compiledRule {
	out := make([]compiledRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, compiledRule{
			kind:   rule.kind,
			strong: compileKeywords(rule.strong),
			weak:   compileKeywords(rule.weak),
		})
	}
	return out
}

func compileKeywords(keywords []string) []compiledKeyword {
	out := make([]compiledKeyword, 0, len(keywords))
	for _, kw := range keywords {
		tokens := normalizeTokens(kw)
		if len(tokens) == 0 {
			continue
		}
		out = append(out, compiledKeyword{
			display: strings.ToLower(strings.TrimSpace(kw)),
			tokens:  tokens,
		})
	}
	return out
}

// Triage implements EmergencyTriage with keyword matching on the
// normalized description. No signal (empty/unknown text) -> zero result.
func (c *OfflineClassifier) Triage(_ context.Context, in TriageInput) (TriageResult, error) {
	if strings.TrimSpace(in.Description) == "" {
		return TriageResult{}, nil
	}
	text := normalizeTokens(in.Description)
	if len(text) == 0 {
		return TriageResult{}, nil
	}

	scores := make(map[string]int, len(compiledRules))
	evidence := make(map[string]string, len(compiledRules))
	for _, rule := range compiledRules {
		for _, kw := range rule.strong {
			if phraseMatches(text, kw.tokens) {
				scores[rule.kind] += strongWeight
				if evidence[rule.kind] == "" {
					evidence[rule.kind] = kw.display
				}
			}
		}
		for _, kw := range rule.weak {
			if phraseMatches(text, kw.tokens) {
				scores[rule.kind] += weakWeight
				if evidence[rule.kind] == "" {
					evidence[rule.kind] = kw.display
				}
			}
		}
	}
	if len(scores) == 0 {
		return TriageResult{}, nil
	}

	severity := severityBase
	for _, kw := range compiledUrgent {
		if phraseMatches(text, kw.tokens) {
			severity += severityBoost
		}
	}
	if severity > severityCap {
		severity = severityCap
	}

	kind := pickEmergencyType(scores)
	return TriageResult{
		EmergencyType: kind,
		Severity:      severity,
		Summary:       summarizeTriage(kind, severity, evidence[kind]),
	}, nil
}

// pickEmergencyType returns the rule with the highest weighted score;
// ties resolve by life-safety priority: MEDICAL > DANGER > ACCIDENT.
func pickEmergencyType(scores map[string]int) string {
	best, bestScore := "", 0
	for _, kind := range []string{entity.EmergencyTypeMedical, entity.EmergencyTypeDanger, entity.EmergencyTypeAccident} {
		if s := scores[kind]; s > bestScore {
			best, bestScore = kind, s
		}
	}
	return best
}

func summarizeTriage(kind string, severity float64, evidence string) string {
	var line string
	switch kind {
	case entity.EmergencyTypeMedical:
		line = "Kemungkinan darurat medis"
	case entity.EmergencyTypeAccident:
		line = "Kemungkinan kecelakaan"
	default:
		line = "Potensi bahaya atau ancaman"
	}
	if evidence != "" {
		line += " (" + evidence + ")"
	}
	if severity >= 0.8 {
		return "PRIORITAS TINGGI: " + line
	}
	return line
}

// normalizeTokens lowercases the text, turns any non-alphanumeric rune
// into a separator, and splits it into tokens. Stretched letters are
// handled by tokenMatches (see squeezeRepeats) so Indonesian suffixes
// stay intact.
func normalizeTokens(s string) []string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	return strings.Fields(b.String())
}

// squeezeRepeats drops runs of identical letters, so "tolonggg" and
// "pingsannn" still compare equal to "tolong" and "pingsan".
func squeezeRepeats(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var prev rune = -1
	for _, r := range s {
		if r == prev {
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

// wordSuffixes are the Indonesian/English endings a token may carry on
// top of a keyword ("pingsannya", "kecelakaannya", "attacked"). Only
// whole suffixes are accepted, so "motor" does not match "motorik" and
// "raped" does not match "scraped".
var wordSuffixes = []string{
	"nya", "ku", "mu", "lah", "kah", "pun", "an", "i", "kan",
	"s", "es", "ed", "ing", "er", "ers", "d",
}

func tokenMatches(textToken, keywordToken string) bool {
	if matchesWithSuffix(textToken, keywordToken) {
		return true
	}
	// Repeat-insensitive fallback for stretched typing ("pingsannn").
	return matchesWithSuffix(squeezeRepeats(textToken), squeezeRepeats(keywordToken))
}

func matchesWithSuffix(textToken, keywordToken string) bool {
	if textToken == keywordToken {
		return true
	}
	if !strings.HasPrefix(textToken, keywordToken) {
		return false
	}
	rest := textToken[len(keywordToken):]
	for _, suffix := range wordSuffixes {
		if rest == suffix {
			return true
		}
	}
	return false
}

// phraseMatches reports whether the keyword tokens appear in order and
// adjacent somewhere in the text.
func phraseMatches(text, phrase []string) bool {
	if len(phrase) == 0 || len(text) < len(phrase) {
		return false
	}
	for i := 0; i+len(phrase) <= len(text); i++ {
		ok := true
		for j, keyword := range phrase {
			if !tokenMatches(text[i+j], keyword) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
