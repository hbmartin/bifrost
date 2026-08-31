package vectorstore

import (
	"math/rand"
	"os"

	"github.com/google/uuid"
)

// Helper functions
func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func generateUUID() string {
	return uuid.New().String()
}

func generateTestEmbedding(dim int) []float32 {
	embedding := make([]float32, dim)
	for i := range embedding {
		embedding[i] = rand.Float32()*2 - 1 // Random values between -1 and 1
	}
	return embedding
}

func generateSimilarEmbedding(original []float32, similarity float32) []float32 {
	similar := make([]float32, len(original))
	for i := range similar {
		// Add small random noise to create similar but not identical embedding
		noise := (rand.Float32()*2 - 1) * (1 - similarity) * 0.1
		similar[i] = original[i] + noise
	}
	return similar
}
