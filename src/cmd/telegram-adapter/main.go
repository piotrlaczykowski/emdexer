package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

type TelegramUpdate struct {
	UpdateID int `json:"update_id"`
	Message  struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	gatewayURL := os.Getenv("GATEWAY_URL") // http://gateway:7700/v1/chat/completions
	authKey := os.Getenv("Emdexer_AUTH_KEY")

	offset := 0
	for {
		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", token, offset)
		resp, err := http.Get(url)
		if err != nil {
			log.Println(err)
			continue
		}

		var result struct {
			OK     bool             `json:"ok"`
			Result []TelegramUpdate `json:"result"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		for _, u := range result.Result {
			offset = u.UpdateID + 1
			log.Printf("Telegram: received %q from %d", u.Message.Text, u.Message.Chat.ID)

			// Forward to Gateway
			payload, _ := json.Marshal(map[string]interface{}{
				"model": "emdexer",
				"messages": []map[string]string{
					{"role": "user", "content": u.Message.Text},
				},
			})
			req, _ := http.NewRequest("POST", gatewayURL, bytes.NewBuffer(payload))
			req.Header.Set("Authorization", "Bearer "+authKey)
			req.Header.Set("Content-Type", "application/json")

			client := &http.Client{}
			gResp, err := client.Do(req)
			if err != nil {
				log.Println(err)
				continue
			}

			var gRes struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			json.NewDecoder(gResp.Body).Decode(&gRes)
			gResp.Body.Close()

			if len(gRes.Choices) > 0 {
				ans := gRes.Choices[0].Message.Content
				sendURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
				sendPayload, _ := json.Marshal(map[string]interface{}{
					"chat_id": u.Message.Chat.ID,
					"text":    ans,
				})
				http.Post(sendURL, "application/json", bytes.NewBuffer(sendPayload))
			}
		}
	}
}
