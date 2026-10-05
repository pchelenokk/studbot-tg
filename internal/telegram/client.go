package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type Client struct {
	token  string
	http   *http.Client
	apiURL string
}

func NewClientWithProxy(token, proxyURL string) *Client {
	transport := &http.Transport{
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &Client{
		token: token,
		http: &http.Client{
			Timeout:   60 * time.Second,
			Transport: transport,
		},
		apiURL: fmt.Sprintf("https://api.telegram.org/bot%s", token),
	}
}

func (c *Client) apiURLFor(method string) string {
	return fmt.Sprintf("%s/%s", c.apiURL, method)
}

func (c *Client) do(method string, payload interface{}) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequest("POST", c.apiURLFor(method), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram api error: %s", string(respBody))
	}

	return respBody, nil
}

func (c *Client) GetFile(fileID string) (string, error) {
	payload := map[string]string{"file_id": fileID}
	respBody, err := c.do("getFile", payload)
	if err != nil {
		return "", err
	}

	var result struct {
		OK   bool `json:"ok"`
		File struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if !result.OK {
		return "", fmt.Errorf("getFile failed")
	}
	return result.File.FilePath, nil
}

func (c *Client) DownloadFile(filePath, destDir, destName string) (string, error) {
	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", c.token, filePath)
	resp, err := c.http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %d", resp.StatusCode)
	}

	destPath := filepath.Join(destDir, destName)
	f, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	if err != nil {
		return "", err
	}
	return destPath, nil
}

func (c *Client) SetChatMenuButton(chatID int64, text, url string) error {
	payload := map[string]interface{}{
		"menu_button": map[string]interface{}{
			"type": "web_app",
			"text": text,
			"web_app": map[string]interface{}{
				"url": url,
			},
		},
	}
	if chatID != 0 {
		payload["chat_id"] = chatID
	}
	_, err := c.do("setChatMenuButton", payload)
	return err
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

func (c *Client) SetMyCommands(commands []BotCommand) error {
	payload := map[string]interface{}{
		"commands": commands,
	}
	_, err := c.do("setMyCommands", payload)
	return err
}

func (c *Client) AnswerCallbackQuery(callbackID, text string, showAlert bool) error {
	payload := map[string]interface{}{
		"callback_query_id": callbackID,
		"text":              text,
		"show_alert":        showAlert,
	}
	_, err := c.do("answerCallbackQuery", payload)
	return err
}
