package embedding

import (
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

const (
	// MaxSingleEmbeddingLength is ~2000 words / 8000 chars, well within the 8191 token limit of modern models.
	MaxSingleEmbeddingLength = 8000
	// ChunkOverlapChars is the sliding window overlap for paragraph continuity.
	ChunkOverlapChars = 400
)

// PrepareObservationText builds the dense text payload for embedding.
// It enriches the text with contextual metadata (Anthropic Contextual Retrieval technique)
// so dense vector search retrieves relevant memories even with brief query keywords.
func PrepareObservationText(obs *domain.Observation) string {
	if obs == nil {
		return ""
	}
	var sb strings.Builder
	if obs.Title != "" {
		sb.WriteString(obs.Title)
		sb.WriteString("\n")
	}
	if obs.Type != "" || obs.Project != "" || obs.TopicKey != "" {
		sb.WriteString("[Context: ")
		if obs.Type != "" {
			sb.WriteString("type=")
			sb.WriteString(obs.Type)
			sb.WriteString(" ")
		}
		if obs.Project != "" {
			sb.WriteString("project=")
			sb.WriteString(obs.Project)
			sb.WriteString(" ")
		}
		if obs.TopicKey != "" {
			sb.WriteString("topic=")
			sb.WriteString(obs.TopicKey)
			sb.WriteString(" ")
		}
		sb.WriteString("]\n")
	}
	sb.WriteString(obs.Content)
	return strings.TrimSpace(sb.String())
}

// ChunkText splits large content into overlapping chunks if it exceeds maxChars.
func ChunkText(text string, maxChars, overlapChars int) []string {
	text = strings.TrimSpace(text)
	if len(text) <= maxChars || maxChars <= 0 {
		if text == "" {
			return nil
		}
		return []string{text}
	}
	if overlapChars >= maxChars {
		overlapChars = maxChars / 4
	}

	var chunks []string
	runes := []rune(text)
	start := 0
	total := len(runes)

	for start < total {
		end := start + maxChars
		if end >= total {
			end = total
		} else {
			// Find nearest sentence or newline break before cut-off
			for i := end; i > start+maxChars/2; i-- {
				if runes[i-1] == '\n' || runes[i-1] == '.' || runes[i-1] == ';' {
					end = i
					break
				}
			}
		}
		chunk := string(runes[start:end])
		if trimmed := strings.TrimSpace(chunk); len(trimmed) > 0 {
			chunks = append(chunks, trimmed)
		}
		if end == total {
			break
		}
		start = end - overlapChars
		if start < 0 {
			start = 0
		}
	}
	return chunks
}
