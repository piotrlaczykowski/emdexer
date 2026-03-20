package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
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
	gatewayURL := os.Getenv("GATEWAY_URL")
	authKey := os.Getenv("Emdexer_AUTH_KEY")

	client := &http.Client{
		Timeout: 40 * time.Second,
	}

	offset := 0
	for {
		// #nosec G107 - Dynamic URL for Telegram API offset is required
		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", token, offset)
		resp, err := client.Get(url)
		if err != nil {
			log.Println("Telegram getUpdates error:", err)
			time.Sleep(5 * time.Second)
			continue
		}

		var result struct {
			OK     bool             `json:"ok"`
			Result []TelegramUpdate `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			log.Println("Telegram decode error:", err)
		}
		resp.Body.Close()

		for _, u := range result.Result {
			offset = u.UpdateID + 1
			log.Printf("Telegram: received %q from %d", u.Message.Text, u.Message.Chat.ID)

			payload, _ := json.Marshal(map[string]interface{}{
				"model": "emdexer",
				"messages": []map[string]string{
					{"role": "user", "content": u.Message.Text},
				},
			})

			// #nosec G107 - gatewayURL is internal configuration
			req, err := http.NewRequest("POST", gatewayURL, bytes.NewBuffer(payload))
			if err != nil {
				log.Println("Request creation error:", err)
				continue
			}
			req.Header.Set("Authorization", "Bearer "+authKey)
			req.Header.Set("Content-Type", "application/json")

			gResp, err := client.Do(req)
			if err != nil {
				log.Println("Gateway call error:", err)
				continue
			}

			var gRes struct {
				Choices []struct {
					Message struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
			}
			if err := json.NewDecoder(gResp.Body).Decode(&gRes); err != nil {
				log.Println("Gateway decode error:", err)
			}
			gResp.Body.Close()

			if len(gRes.Choices) > 0 {
				ans := gRes.Choices[0].Message.Content
				// #nosec G107 - sendURL is standard Telegram endpoint
				sendURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
				sendPayload, _ := json.Marshal(map[string]interface{}{
					"chat_id": u.Message.Chat.ID,
					"text":    ans,
				})
				_, err := client.Post(sendURL, "application/json", bytes.NewBuffer(sendPayload))
				if err != nil {
					log.Println("Telegram send error:", err)
				}
			}
		}
	}
}
