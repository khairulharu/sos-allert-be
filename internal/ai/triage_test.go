package ai

import (
	"strings"
	"testing"
)

func TestClassifierCases(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		category string
		severity int
		lang     string
	}{
		{
			name:     "english motorbike bleeding",
			text:     "Help! My friend fell off his scooter near Batu Bolong, leg bleeding, can't move",
			category: CategoryAccidentTraffic, severity: 5, lang: "en",
		},
		{
			name:     "indonesian drowning",
			text:     "Tolong, ada orang tenggelam di pantai, terseret ombak",
			category: CategoryDrowning, severity: 5, lang: "id",
		},
		{
			name:     "indonesian traffic accident",
			text:     "Kecelakaan motor di jalan raya, korban patah kaki",
			category: CategoryAccidentTraffic, severity: 4, lang: "id",
		},
		{
			name:     "fire",
			text:     "Kebakaran di rumah, api membesar",
			category: CategoryFire, severity: 4, lang: "id",
		},
		{
			name:     "earthquake",
			text:     "Gempa besar, banyak orang panik",
			category: CategoryNaturalDisaster, severity: 4, lang: "id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Classifier{}.Classify(tc.text)
			if r.Category != tc.category {
				t.Errorf("category = %q, want %q", r.Category, tc.category)
			}
			if r.Severity != tc.severity {
				t.Errorf("severity = %d, want %d", r.Severity, tc.severity)
			}
			if r.Language != tc.lang {
				t.Errorf("language = %q, want %q", r.Language, tc.lang)
			}
			if r.Source != "rules" {
				t.Errorf("source = %q, want rules", r.Source)
			}
			if r.SummaryID == "" || r.SummaryEN == "" {
				t.Error("summaries must not be empty")
			}
			if !strings.Contains(r.SummaryID, "Tolong") && !strings.Contains(r.SummaryID, "Kecelakaan") && !strings.Contains(r.SummaryID, "Orang") && !strings.Contains(r.SummaryID, "Kebakaran") && !strings.Contains(r.SummaryID, "Bencana") {
				t.Logf("rule summary (template + quote): %q", r.SummaryID)
			}
		})
	}
}

func TestClassifierAmbiguousEscalates(t *testing.T) {
	r := Classifier{}.Classify("sakit")
	if r.Category != CategoryMedical && r.Category != CategoryOther {
		t.Errorf("ambiguous category = %q", r.Category)
	}
	if r.Severity < 4 {
		t.Errorf("ambiguous report severity = %d, want escalated to at least 4", r.Severity)
	}
	if !r.NeedsClarification {
		t.Error("ambiguous report must need clarification")
	}
}

func TestValidateClampsAndEscalates(t *testing.T) {
	r := Validate(Result{Category: "NOPE", Severity: 99, Confidence: 0.2, Source: "llm"})
	if r.Category != CategoryOther {
		t.Errorf("category = %q, want OTHER", r.Category)
	}
	if r.Severity != 5 {
		t.Errorf("severity = %d, want clamped to 5", r.Severity)
	}
	if r.SummaryID == "" || r.SummaryEN == "" {
		t.Error("empty summaries must be filled from the category label")
	}
	low := Validate(Result{Category: CategoryOther, Severity: 2, Confidence: 0.3})
	if low.Severity < 4 || !low.NeedsClarification {
		t.Errorf("low-confidence result must escalate: %+v", low)
	}
}

func TestGuideFor(t *testing.T) {
	for _, cat := range []string{CategoryAccidentTraffic, CategoryMedical, CategoryDrowning, CategoryFire, CategoryDanger, CategoryNaturalDisaster, CategoryOther, "BOGUS"} {
		g := GuideFor(cat)
		if len(g.StepsID) == 0 || len(g.StepsEN) == 0 {
			t.Errorf("guide for %q has no steps", cat)
		}
		if g.DisclaimerID == "" || g.DisclaimerEN == "" {
			t.Errorf("guide for %q has no disclaimer", cat)
		}
	}
	if GuideFor(CategoryDrowning).TitleEN == GuideFor(CategoryFire).TitleEN {
		t.Error("guides must differ by category")
	}
}

func TestNeedsForCategory(t *testing.T) {
	if len(NeedsForCategory(CategoryDrowning)) == 0 {
		t.Error("drowning needs must not be empty")
	}
}
