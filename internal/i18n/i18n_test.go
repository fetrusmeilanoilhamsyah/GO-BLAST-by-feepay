package i18n

import (
	"testing"
)

func TestI18nCompleteness(t *testing.T) {
	// Need to load from ../../locales
	err := Load("../../locales")
	if err != nil {
		t.Fatalf("Failed to load locales: %v", err)
	}

	trans := GetTranslations()
	idKeys := getKeys(trans["id"])
	enKeys := getKeys(trans["en"])
	zhKeys := getKeys(trans["zh"])

	compareKeys(t, "id", idKeys, "en", enKeys)
	compareKeys(t, "id", idKeys, "zh", zhKeys)
}

func getKeys(m map[string]string) map[string]bool {
	keys := make(map[string]bool)
	for k := range m {
		if k == "_status" {
			continue
		}
		keys[k] = true
	}
	return keys
}

func compareKeys(t *testing.T, lang1 string, keys1 map[string]bool, lang2 string, keys2 map[string]bool) {
	for k := range keys1 {
		if !keys2[k] {
			t.Errorf("Key '%s' is in %s.json but missing in %s.json", k, lang1, lang2)
		}
	}
	for k := range keys2 {
		if !keys1[k] {
			t.Errorf("Key '%s' is in %s.json but missing in %s.json", k, lang2, lang1)
		}
	}
}

func TestT(t *testing.T) {
	Load("../../locales")
	
	// Test basic
	if got := T("id", "app.name"); got != "GO BLAST" {
		t.Errorf("T(id, app.name) = %v", got)
	}
	
	// Test fallback
	// Add a dummy key to id for test
	trans := GetTranslations()
	trans["id"]["test.fallback"] = "Jatuh"
	
	if got := T("en", "test.fallback"); got != "Jatuh" {
		t.Errorf("T(en, test.fallback) expected 'Jatuh', got %v", got)
	}
}
