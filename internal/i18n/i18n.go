package i18n

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

var translations map[string]map[string]string

func Load(localesDir string) error {
	translations = make(map[string]map[string]string)
	langs := []string{"id", "en", "zh"}

	for _, lang := range langs {
		path := filepath.Join(localesDir, lang+".json")
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		var data map[string]string
		if err := json.Unmarshal(b, &data); err != nil {
			return fmt.Errorf("failed to unmarshal %s: %w", path, err)
		}

		translations[lang] = data
	}

	return nil
}

// T translates a key into the given language.
// Falls back to "id" if the key doesn't exist in the target language.
func T(lang, key string) string {
	if translations == nil {
		return key
	}

	langMap, ok := translations[lang]
	if ok {
		if val, exists := langMap[key]; exists {
			return val
		}
	}

	// fallback to id
	idMap, ok := translations["id"]
	if ok {
		if val, exists := idMap[key]; exists {
			return val
		}
	}

	return key
}

// GetTranslations returns the raw translations map (mostly for testing).
func GetTranslations() map[string]map[string]string {
	return translations
}
