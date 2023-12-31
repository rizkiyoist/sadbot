package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	openai "github.com/sashabaranov/go-openai"
	"gopkg.in/yaml.v3"
)

type Config struct {
	GPT           string `yaml:"gpt"`
	Bot           string `yaml:"bot"`
	HistoryLimit  int    `yaml:"history_limit"`
	ChatPerMinute int    `yaml:"chat_per_minute"`
}

var lastChatTime map[string]int // map of string time format 15:04 to count of chats
var limitChar int = 1500
var maxTokens int = 1500

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

	log.Printf("Authorized on account %s", bot.Self.UserName)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	LimitedSlice := NewLimitedSlice(config.HistoryLimit)
	lastChatTime = make(map[string]int)

	for update := range updates {
		now := time.Now()
		nowString := now.Format("15:04")
		nowLastSecond := now.Second()
		lastChatTime[nowString]++

		// handling in case user don't have a telegram username
		userName := update.Message.From.UserName + "-" + update.Message.From.FirstName + "-" + update.Message.From.LastName

		// var prompt string
		LimitedSlice.Add(update.Message.Text)

		var prompt string

		for _, ls := range LimitedSlice.Get() {
			prompt = prompt + ls + "\n"
		}

		if update.Message != nil { // If we got a message
			// limit size of prompt otherwise model might return error if it's too long
			// not the best solution since this limits by character instead of token
			// TODO: summarize the old chat
			if len(prompt) > limitChar {
				prompt = prompt[limitChar:]
			}

			// limit size of initial condition otherwise model might return error if it's too long
			if len(initialCond) > limitChar {
				initialCond = initialCond[:limitChar]
			}

			prompt = initialCond + prompt

			if update.Message.IsCommand() {
				if update.Message.Text == "/debug" {
					fmt.Printf("Debug start ---------- \n %v \n ----------- debug end\n", prompt)
					continue
				}
			}

			if update.Message.IsCommand() {
				if update.Message.Text == "/clear" {
					LimitedSlice = NewLimitedSlice(config.HistoryLimit)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Memory cleared")
					msg.ReplyToMessageID = update.Message.MessageID
					bot.Send(msg)
					continue
				}
			}

			if update.Message.IsCommand() {
				if update.Message.Text == "/uplimit" {
					limitChar = limitChar + 500
					LimitedSlice = NewLimitedSlice(config.HistoryLimit)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Character limit increased to "+fmt.Sprint(limitChar))
					msg.ReplyToMessageID = update.Message.MessageID
					bot.Send(msg)
					continue
				}
			}

			if update.Message.IsCommand() {
				if update.Message.Text == "/downlimit" {
					limitChar = limitChar - 500
					LimitedSlice = NewLimitedSlice(config.HistoryLimit)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Character limit decreased to "+fmt.Sprint(limitChar))
					msg.ReplyToMessageID = update.Message.MessageID
					bot.Send(msg)
					continue
				}
			}

			if lastChatTime[nowString] >= config.ChatPerMinute {
				// sleep for the rest of the minute
				time.Sleep(time.Duration(60-nowLastSecond) * time.Second)
				fmt.Println("sleeping")
				lastChatTime = make(map[string]int) // reinitialize map to clear old map value
			}

			// prompting
			resp := ask(c, prompt, openai.ChatMessageRoleFunction, userName)
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
		MaxTokens: maxTokens,
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
	fmt.Print("\nStreaming")
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
		fmt.Print(".")
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
