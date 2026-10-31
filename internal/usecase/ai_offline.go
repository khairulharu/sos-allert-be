package usecase

import (
	"context"
	"strings"

	"golang-clean-architecture/internal/entity"
)

// OfflineClassifier is the deterministic, dependency-free AI triage
// fallback: a keyword classifier for Indonesian/English distress
// messages. It never errors, so the SOS trigger always has a baseline
// classification even when the LLM provider is down or unconfigured.
// An empty description yields a zero result (no signal), keeping the
// legacy trigger behaviour byte-for-byte identical.
type OfflineClassifier struct{}

func NewOfflineClassifier() *OfflineClassifier { return &OfflineClassifier{} }

type classifierRule struct {
	kind     string
	keywords []string
}

var classifierRules = []classifierRule{
	{kind: entity.EmergencyTypeMedical, keywords: []string{
		"pingsan", "sakit", "jantung", "sesak napas", "sesak nafas", "napas", "nafas",
		"ambulan", "ambulans", "medis", "darah", "berdarah", "pendarahan", "luka", "demam",
		"kejang", "stroke", "sick", "ill", "heart attack", "breathing", "medical",
		"ambulance", "faint", "fainted", "unconscious", "bleeding", "seizure",
	}},
	{kind: entity.EmergencyTypeAccident, keywords: []string{
		"kecelakaan", "tabrakan", "tertabrak", "jatuh", "tumbang", "bentur", "crash",
		"accident", "collision", "vehicle", "motor", "mobil", "kendaraan",
		"motorcycle", "car crash", "fell", "fell down",
	}},
	{kind: entity.EmergencyTypeDanger, keywords: []string{
		"begal", "maling", "pencuri", "perampok", "diculik", "diancam", "teror", "dikejar",
		"anjing", "pisau", "parang", "senjata", "tawuran", "kekerasan", "robber", "thief",
		"kidnap", "threat", "weapon", "knife", "stabbed", "dog", "fighting",
	}},
}

var urgentSeverityKeywords = []string{
	"kritis", "darurat", "segera", "parah", "tidak sadar", "banyak darah", "banyak korban",
	"emergency", "critical", "urgent", "unconscious", "bleeding severely", "sekarang",
}

// severityBase is the score of any classified message; each urgent
// keyword adds severityBoost, capped at 1.
const (
	severityBase  = 0.6
	severityBoost = 0.2
	severityCap   = 1.0
)

// Triage implements EmergencyTriage with pure substring matching on a
// lowercased description. No signal (empty/unknown text) -> zero result.
func (c *OfflineClassifier) Triage(_ context.Context, in TriageInput) (TriageResult, error) {
	if strings.TrimSpace(in.Description) == "" {
		return TriageResult{}, nil
	}
	text := strings.ToLower(in.Description)

	scores := make(map[string]int, len(classifierRules))
	for _, rule := range classifierRules {
		for _, kw := range rule.keywords {
			if strings.Contains(text, kw) {
				scores[rule.kind]++
			}
		}
	}
	if len(scores) == 0 {
		return TriageResult{}, nil
	}

	severity := severityBase
	for _, kw := range urgentSeverityKeywords {
		if strings.Contains(text, kw) {
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
		Summary:       summarizeTriage(kind, severity),
	}, nil
}

// pickEmergencyType returns the rule with the most keyword hits; ties
// resolve by life-safety priority: MEDICAL > DANGER > ACCIDENT.
func pickEmergencyType(scores map[string]int) string {
	best, bestScore := "", 0
	for _, kind := range []string{entity.EmergencyTypeMedical, entity.EmergencyTypeDanger, entity.EmergencyTypeAccident} {
		if s := scores[kind]; s > bestScore {
			best, bestScore = kind, s
		}
	}
	return best
}

func summarizeTriage(kind string, severity float64) string {
	var line string
	switch kind {
	case entity.EmergencyTypeMedical:
		line = "Kemungkinan darurat medis"
	case entity.EmergencyTypeAccident:
		line = "Kemungkinan kecelakaan"
	default:
		line = "Potensi bahaya atau ancaman"
	}
	if severity >= 0.8 {
		return "PRIORITAS TINGGI: " + line
	}
	return line
}
