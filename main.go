package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	openai "github.com/sashabaranov/go-openai"
	"gopkg.in/yaml.v3"
)

type Config struct {
	GPT          string
	Bot          string
	HistoryLimit int
}

func main() {
	yamlFile, err := os.ReadFile("env.yaml")
	if err != nil {
		panic(err)
	}
	var config Config
	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		panic(err)
	}
	// get initial condition for the prompt
	content, err := os.ReadFile("initialcond.txt")
	if err != nil {
		log.Fatal(err)
	}
	initialCond := string(content)

	// create a new ChatGPT client
	c := openai.NewClient(config.GPT)

	bot, err := tgbotapi.NewBotAPI(config.Bot)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	LimitedSlice := NewLimitedSlice(config.HistoryLimit)

	for update := range updates {
		if update.Message != nil { // If we got a message
			log.Printf("[%s] %s \n", update.Message.From.UserName, update.Message.Text)

			// var prompt string
			LimitedSlice.Add(update.Message.Text)

			var prompt string
			prompt = initialCond + prompt

			for _, ls := range LimitedSlice.Get() {
				prompt = prompt + ls + "\n"
			}

			// prompting
			resp := ask(c, prompt, openai.ChatMessageRoleFunction, update.Message.From.UserName)
			LimitedSlice.Add(resp)
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, resp)
			msg.ReplyToMessageID = update.Message.MessageID

			bot.Send(msg)
		}
	}
}

func ask(c *openai.Client, prompt string, role string, name string) string {
	ctx := context.Background()
	req := openai.ChatCompletionRequest{
		Model:     openai.GPT3Dot5Turbo,
		MaxTokens: 2000,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    role,
				Content: prompt,
				Name:    name,
			},
		},
		Stream: true,
	}
	stream, err := c.CreateChatCompletionStream(ctx, req)
	if err != nil {
		fmt.Printf("ChatCompletionStream error: %v\n", err)
		return "error"
	}
	defer stream.Close()

	var fullText string
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			fmt.Println("\nStream finished")
			break
		}
		if err != nil {
			fmt.Printf("\nStream error: %v\n", err)
			break
		}
		fmt.Print(response.Choices[0].Delta.Content)
		fullText = fullText + response.Choices[0].Delta.Content
	}
	if len(fullText) < 10 {
		return "error"
	}
	return fullText
}

type LimitedSlice struct {
	data   []string
	limit  int
	cursor int
}

func NewLimitedSlice(limit int) *LimitedSlice {
	return &LimitedSlice{
		data:   make([]string, limit),
		limit:  limit,
		cursor: 0,
	}
}

func (ls *LimitedSlice) Add(element string) {
	ls.data[ls.cursor] = element
	ls.cursor = (ls.cursor + 1) % ls.limit
}

func (ls *LimitedSlice) Get() []string {
	result := make([]string, 0, ls.limit)
	for i := 0; i < ls.limit; i++ {
		idx := (ls.cursor + i) % ls.limit
		if ls.data[idx] != "" {
			result = append(result, ls.data[idx])
		}
	}
	return result
}
