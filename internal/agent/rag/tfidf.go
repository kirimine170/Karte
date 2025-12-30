package rag

import (
	"math"
	"strings"
	"sync"
	"unicode"

	"github.com/ikawaha/kagome-dict/ipa"
	"github.com/ikawaha/kagome/v2/tokenizer"
)

var (
	// kagomeTokenizer is a singleton tokenizer instance
	kagomeTokenizer *tokenizer.Tokenizer
	kagomeOnce      sync.Once
)

// getKagomeTokenizer returns a singleton kagome tokenizer instance
func getKagomeTokenizer() *tokenizer.Tokenizer {
	kagomeOnce.Do(func() {
		t, err := tokenizer.New(ipa.Dict(), tokenizer.OmitBosEos())
		if err != nil {
			// If initialization fails, tokenizer will be nil
			// Fall back to simple tokenization
			return
		}
		kagomeTokenizer = t
	})
	return kagomeTokenizer
}

// scriptType represents the type of script
type scriptType int

const (
	scriptHiragana scriptType = iota
	scriptKatakana
	scriptKanji
	scriptLatin
	scriptNumber
	scriptOther
)

// getScriptType determines the script type of a rune
func getScriptType(r rune) scriptType {
	switch {
	case r >= 0x3040 && r <= 0x309F:
		return scriptHiragana
	case r >= 0x30A0 && r <= 0x30FF:
		return scriptKatakana
	case r >= 0x4E00 && r <= 0x9FAF:
		return scriptKanji
	case unicode.IsLetter(r):
		return scriptLatin
	case unicode.IsNumber(r):
		return scriptNumber
	default:
		return scriptOther
	}
}

// isJapanese checks if a text contains Japanese characters
func isJapanese(text string) bool {
	for _, r := range text {
		if (r >= 0x3040 && r <= 0x309F) || // Hiragana
			(r >= 0x30A0 && r <= 0x30FF) || // Katakana
			(r >= 0x4E00 && r <= 0x9FAF) { // CJK ideographs
			return true
		}
	}
	return false
}

// tokenizeJapanese uses kagome for morphological analysis
func tokenizeJapanese(text string) []string {
	t := getKagomeTokenizer()
	if t == nil {
		// Fallback to simple tokenization if kagome is not available
		return tokenizeSimple(text)
	}

	var tokens []string
	segments := t.Tokenize(text)
	
	for _, segment := range segments {
		// Extract content words (nouns, verbs, adjectives, etc.)
		// Skip particles, auxiliary verbs, and other function words
		pos := segment.Features()
		if len(pos) == 0 {
			continue
		}
		
		// Extract nouns, verbs, adjectives, adverbs, and proper nouns
		// Skip particles (助詞), auxiliary verbs (助動詞), etc.
		pos1 := pos[0]
		if pos1 == "名詞" || pos1 == "動詞" || pos1 == "形容詞" || pos1 == "副詞" || pos1 == "連体詞" {
			surface := segment.Surface
			if surface != "" && !isStopword(surface) {
				tokens = append(tokens, strings.ToLower(surface))
			}
		}
	}
	
	return tokens
}

// tokenizeSimple tokenizes text using script boundary detection (fallback)
func tokenizeSimple(text string) []string {
	var tokens []string
	var current strings.Builder
	var currentScript scriptType = scriptOther

	for _, r := range text {
		script := getScriptType(r)
		
		// Skip non-word characters
		if script == scriptOther {
			if current.Len() > 0 {
				tokens = append(tokens, strings.ToLower(current.String()))
				current.Reset()
				currentScript = scriptOther
			}
			continue
		}

		// If script type changed, start a new token
		if current.Len() > 0 && currentScript != script {
			tokens = append(tokens, strings.ToLower(current.String()))
			current.Reset()
		}

		current.WriteRune(r)
		currentScript = script
	}

	if current.Len() > 0 {
		tokens = append(tokens, strings.ToLower(current.String()))
	}

	return tokens
}

// tokenize tokenizes text into words using morphological analysis for Japanese
func tokenize(text string) []string {
	if text == "" {
		return []string{}
	}

	// Check if text contains Japanese
	if isJapanese(text) {
		// Use kagome for Japanese text
		tokens := tokenizeJapanese(text)
		// Additional filtering for stopwords (some may have been filtered already)
		return filterStopwords(tokens)
	}

	// Use simple tokenization for non-Japanese text
	tokens := tokenizeSimple(text)
	return filterStopwords(tokens)
}

// calculateTF calculates Term Frequency for a document
func calculateTF(tokens []string) map[string]float64 {
	tf := make(map[string]float64)
	total := float64(len(tokens))

	if total == 0 {
		return tf
	}

	for _, token := range tokens {
		tf[token]++
	}

	// Normalize by document length
	for token := range tf {
		tf[token] = tf[token] / total
	}

	return tf
}

// calculateIDF calculates Inverse Document Frequency across all documents
func calculateIDF(chunks []Chunk) map[string]float64 {
	idf := make(map[string]int)
	
	// Count unique documents (not chunks)
	docSet := make(map[string]bool)
	for _, chunk := range chunks {
		docSet[chunk.DocID] = true
	}
	totalDocs := float64(len(docSet))

	if totalDocs == 0 {
		return make(map[string]float64)
	}

	// Count documents containing each term
	docTerms := make(map[string]map[string]bool)
	for i, chunk := range chunks {
		docID := chunk.DocID
		if docTerms[docID] == nil {
			docTerms[docID] = make(map[string]bool)
		}
		for _, token := range chunk.Tokens {
			if !docTerms[docID][token] {
				docTerms[docID][token] = true
				idf[token]++
			}
		}
		_ = i // suppress unused variable
	}

	// Calculate IDF
	idfScores := make(map[string]float64)
	for token, count := range idf {
		idfScores[token] = math.Log(totalDocs / float64(count))
	}

	return idfScores
}

// calculateTFIDF calculates TF-IDF scores for a chunk
func calculateTFIDF(chunk Chunk, idf map[string]float64) map[string]float64 {
	tf := calculateTF(chunk.Tokens)
	tfidf := make(map[string]float64)

	for token, tfScore := range tf {
		idfScore, exists := idf[token]
		if exists {
			tfidf[token] = tfScore * idfScore
		}
	}

	return tfidf
}

// calculateQueryTFIDF calculates TF-IDF for a query
func calculateQueryTFIDF(queryTokens []string, idf map[string]float64) map[string]float64 {
	tf := calculateTF(queryTokens)
	tfidf := make(map[string]float64)

	for token, tfScore := range tf {
		idfScore, exists := idf[token]
		if exists {
			tfidf[token] = tfScore * idfScore
		}
	}

	return tfidf
}

// cosineSimilarity calculates cosine similarity between two TF-IDF vectors
func cosineSimilarity(vec1, vec2 map[string]float64) float64 {
	dotProduct := 0.0
	magnitude1 := 0.0
	magnitude2 := 0.0

	// Calculate dot product and magnitudes
	allTerms := make(map[string]bool)
	for term := range vec1 {
		allTerms[term] = true
	}
	for term := range vec2 {
		allTerms[term] = true
	}

	for term := range allTerms {
		v1 := vec1[term]
		v2 := vec2[term]
		dotProduct += v1 * v2
		magnitude1 += v1 * v1
		magnitude2 += v2 * v2
	}

	if magnitude1 == 0 || magnitude2 == 0 {
		return 0.0
	}

	return dotProduct / (math.Sqrt(magnitude1) * math.Sqrt(magnitude2))
}

// Tokenize tokenizes text into words (simple implementation for Japanese/English)
// This is a public wrapper for the internal tokenize function.
func Tokenize(text string) []string {
	return tokenize(text)
}

// CalculateTF calculates Term Frequency for a document
// This is a public wrapper for the internal calculateTF function.
func CalculateTF(tokens []string) map[string]float64 {
	return calculateTF(tokens)
}

// CalculateIDF calculates Inverse Document Frequency across all documents
// This is a public wrapper for the internal calculateIDF function.
func CalculateIDF(chunks []Chunk) map[string]float64 {
	return calculateIDF(chunks)
}

// CalculateTFIDF calculates TF-IDF scores for a chunk
// This is a public wrapper for the internal calculateTFIDF function.
func CalculateTFIDF(chunk Chunk, idf map[string]float64) map[string]float64 {
	return calculateTFIDF(chunk, idf)
}

