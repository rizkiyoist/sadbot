package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"gopkg.in/yaml.v3"
)

type Config struct {
	GPT           string `yaml:"gpt"`
	Bot           string `yaml:"bot"`
	HistoryLimit  int    `yaml:"history_limit"`
	ChatPerMinute int    `yaml:"chat_per_minute"`
	Name          string `yaml:"name"`
}

var lastChatTime map[string]int // map of string time format 15:04 to count of chats
var limitChar int = 1700
var promptLimit int = 400 // limit characters just in case

func main() {
	fmt.Println("Starting...")
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic: %v\n", r)
			debug.PrintStack()
			go func() {
				time.Sleep(5 * time.Second)
				main()
			}()
		}
	}()

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

	// contexts is for storing context memories so the AI knows the previous conversation
	contexts := []string{}

	// create a new ChatGPT client
	c := openai.NewClient(
		option.WithAPIKey(config.GPT),
		option.WithBaseURL("https://openrouter.ai/api/v1"),
	)

	bot, err := tgbotapi.NewBotAPI(config.Bot)
	if err != nil {
		log.Panic(err)
	}

	log.Printf("Authorized on account %s", bot.Self.UserName)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	lastChatTime = make(map[string]int)

	for update := range updates {
		now := time.Now()
		nowString := now.Format("15:04")
		nowLastSecond := now.Second()
		lastChatTime[nowString]++

		// handling in case user don't have a telegram username
		userName := "unknown"
		if update.Message == nil {
			continue
		}

		var prompt string

		if update.Message != nil { // If we got a message
			if update.Message.From != nil {
				userNameParts := []string{update.Message.From.UserName, update.Message.From.FirstName, update.Message.From.LastName}
				for _, part := range userNameParts {
					if part != "" {
						userName = part
					}
				}
			}

			// limit size of prompt otherwise model might return error if it's too long
			// not the best solution since this limits by character instead of token
			// TODO: summarize the old chat
			if len(prompt) > limitChar {
				prompt = prompt[limitChar:]
			}

			if update.Message.NewChatMembers != nil {
				prompt = prompt + "Reply with new user guideline, username: " + fmt.Sprint(update.Message.From.UserName) + "name: " + fmt.Sprint(update.Message.From.FirstName) + "\n"
			}

			botName := config.Name

			if botName == "" {
				botName = bot.Self.UserName
			}

			if update.Message.Text != "" {
				if len(update.Message.Text) > promptLimit {
					update.Message.Text = update.Message.Text[:promptLimit]
				}
				prompt = update.Message.Text
				fmt.Printf("%s message content: %s\n", update.Message.From.UserName, update.Message.Text)
			}

			if strings.Contains(update.Message.Text, botName) || strings.Contains(update.Message.Text, strings.ToLower(botName)) {
				prompt = prompt + "You are mentioned by: " + fmt.Sprint(update.Message.From.UserName) + " saying " + fmt.Sprint(update.Message.Text) + "\n"
				fmt.Println("mentioned by: " + fmt.Sprint(update.Message.From.UserName) + " saying " + fmt.Sprint(update.Message.Text))
			}

			if update.Message.IsCommand() {
				switch update.Message.Text {
				case "/debug":
					fmt.Printf("Debug start ---------- \n %v \n ----------- debug end\n", prompt)
					continue
				case "/clear":
					contexts = []string{}
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Memory cleared")
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

			totalContextLength := 0
			trimmedContext := []string{}
			for i := len(contexts) - 1; i >= 0; i-- {
				context := contexts[i]
				totalContextLength += len(context)
				if totalContextLength > limitChar {
					continue
				}
				trimmedContext = append(trimmedContext, context)
			}
			contexts = trimmedContext

			jsonContext, _ := json.Marshal(contexts)

			contexts = append(contexts, prompt)

			// prompting
			finalPrompt := initialCond + "\n" + `context json don't use this unless asked: "` + string(jsonContext) + `"\n Here is the prompt:` + prompt
			resp, err := ask(&c, finalPrompt, userName)
			if err != nil {
				fmt.Printf("failed to prompt: %v\n", err)
			}
			if resp != nil {
				// enable this to save replied context
				// contexts = append(contexts, *resp)
			}

			msg := tgbotapi.NewMessage(update.Message.Chat.ID, *resp)
			msg.ReplyToMessageID = update.Message.MessageID

			bot.Send(msg)
		}
	}
}

func ask(c *openai.Client, prompt string, name string) (*string, error) {
	chatCompletion, err := c.Chat.Completions.New(
		context.TODO(),
		openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage(name + ": " + prompt),
			},
			Model:           "z-ai/glm-4.5-air:free",
			ReasoningEffort: "high",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat completion: %w", err)
	}

	rawString := chatCompletion.Choices[0].Message.Content

	return &rawString, nil
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
