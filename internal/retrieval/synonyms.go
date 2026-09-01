package retrieval

import (
	"regexp"
	"strings"
)

var punctuationRegex = regexp.MustCompile(`[¿?¡!.,;:]`)

// commonTypoCorrections maps common Spanish and technical typos to canonical words.
var commonTypoCorrections = map[string]string{
	"hiso":       "hizo",
	"ase":        "hace",
	"despliege":  "despliegue",
	"revisar":    "revisión",
	"implemento": "implementación",
	"habian":     "habían",
	"erro":       "error",
	"fallo":      "falló",
}

// technicalSynonyms maps keywords to their domain alternatives for expansion.
var technicalSynonyms = map[string][]string{
	"worktree":      {"workspace", "entorno"},
	"workspace":     {"worktree", "entorno"},
	"test":          {"tests", "pruebas", "verificación"},
	"tests":         {"test", "pruebas", "verificación"},
	"prueba":        {"pruebas", "tests", "verificación"},
	"pruebas":       {"prueba", "tests", "verificación"},
	"config":        {"configuración", "settings"},
	"configuracion": {"config", "configuración"},
	"deploy":        {"despliegue", "publicación"},
	"despliegue":    {"deploy", "publicación"},
	"auth":          {"autenticación", "autorización"},
	"authz":         {"autorización", "permisos"},
	"mcp":           {"protocolo", "herramientas"},
	"rag":           {"retrieval", "recuperación", "búsqueda"},
}

// NormalizeQuery cleans and corrects common typos in the search query.
func NormalizeQuery(query string) string {
	cleaned := punctuationRegex.ReplaceAllString(query, " ")
	tokens := strings.Fields(cleaned)
	if len(tokens) == 0 {
		return strings.TrimSpace(query)
	}

	result := make([]string, len(tokens))
	for i, token := range tokens {
		lower := strings.ToLower(token)
		if corrected, exists := commonTypoCorrections[lower]; exists {
			result[i] = corrected
		} else {
			result[i] = token
		}
	}
	return strings.Join(result, " ")
}

// ExpandQuerySynonyms returns an OR-expanded query string containing relevant synonyms.
func ExpandQuerySynonyms(query string) string {
	normalized := NormalizeQuery(query)
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 {
		return normalized
	}

	seen := make(map[string]bool)
	expandedTerms := make([]string, 0, len(tokens)*2)

	for _, token := range tokens {
		lower := strings.ToLower(token)
		if !seen[lower] {
			seen[lower] = true
			expandedTerms = append(expandedTerms, token)
		}
		if syns, ok := technicalSynonyms[lower]; ok {
			for _, s := range syns {
				if !seen[s] {
					seen[s] = true
					expandedTerms = append(expandedTerms, s)
				}
			}
		}
	}

	if len(expandedTerms) <= len(tokens) {
		return strings.Join(tokens, " or ")
	}
	return strings.Join(expandedTerms, " or ")
}
