// +build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/iconidentify/xgrabba/internal/config"
	"github.com/iconidentify/xgrabba/pkg/grok"
)

func main() {
	apiKey := os.Getenv("GROK_API_KEY")
	if apiKey == "" {
		fmt.Println("GROK_API_KEY environment variable is required")
		os.Exit(1)
	}

	cfg := config.GrokConfig{
		APIKey:  apiKey,
		BaseURL: "https://api.x.ai/v1",
		Model:   "grok-3",
		Timeout: 120 * time.Second,
	}
	client := grok.NewClient(cfg)

	// Sample transcript for testing
	transcript := `Today we're going to explore the fascinating history of artificial intelligence.
The field of AI was born in 1956 at a conference at Dartmouth College.
Early pioneers like John McCarthy, Marvin Minsky, and Claude Shannon gathered to discuss the possibility of creating thinking machines.

In the early days, researchers were incredibly optimistic. They believed that fully intelligent machines were just around the corner.
This period saw the development of early programs like ELIZA, a chatbot that could simulate conversation.

However, the field hit several "AI winters" - periods of reduced funding and interest due to unmet expectations.
The first major AI winter occurred in the 1970s when early promises failed to materialize.

The renaissance of AI began with the rise of machine learning in the 2000s.
Neural networks, inspired by the human brain, became increasingly powerful.
Deep learning, a subset of machine learning using many-layered neural networks, revolutionized the field.

Today, AI powers everything from voice assistants to autonomous vehicles.
Large language models like GPT and Claude represent the cutting edge of natural language processing.
The future of AI holds both tremendous promise and significant challenges that society must address.`

	fmt.Println("Testing essay generation with live Grok API...")
	fmt.Println("Transcript length:", len(transcript), "characters")
	fmt.Println()

	req := grok.EssayRequest{
		Transcript:  transcript,
		ContentType: "documentary",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := client.GenerateEssay(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		fmt.Printf("ERROR: Essay generation failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== ESSAY GENERATED SUCCESSFULLY ===")
	fmt.Printf("Time taken: %v\n", elapsed)
	fmt.Printf("Title: %s\n", resp.Title)
	fmt.Printf("Word count: %d\n", resp.WordCount)
	fmt.Println()
	fmt.Println("=== ESSAY CONTENT ===")
	fmt.Println(resp.Essay)
	fmt.Println()
	fmt.Println("=== TEST PASSED ===")
}
