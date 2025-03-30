package main

import (
	_ "bytes"
	"encoding/json"
	"fmt"
	_ "io"
	"log"
	"net/http"
	_ "net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/tarantool/go-tarantool"
)

type Bot struct {
	client        *model.Client4
	tarantool     *tarantool.Connection
	user          *model.User
	team          *model.Team
	commands      map[string]CommandHandler
	tarantoolOpts tarantool.Opts
}

type CommandHandler func(req CommandRequest) (string, error)

type CommandRequest struct {
	Token       string `json:"token"`
	TeamID      string `json:"team_id"`
	TeamDomain  string `json:"team_domain"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	UserID      string `json:"user_id"`
	UserName    string `json:"user_name"`
	Command     string `json:"command"`
	Text        string `json:"text"`
	ResponseURL string `json:"response_url"`
	TriggerID   string `json:"trigger_id"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting bot initialization...")

	mattermost := os.Getenv("SERVER")
	if mattermost == "" {
		mattermost = "http://host.docker.internal:8065"
	}

	bot := &Bot{
		client:   model.NewAPIv4Client(mattermost),
		commands: make(map[string]CommandHandler),
		tarantoolOpts: tarantool.Opts{
			User:          os.Getenv("TARANTOOL_USER"),
			Pass:          os.Getenv("TARANTOOL_PASSWORD"),
			Timeout:       10 * time.Second,
			Reconnect:     2 * time.Second,
			SkipSchema:    true,
			MaxReconnects: 5,
		},
	}

	if err := bot.initialize(); err != nil {
		log.Fatalf("Initialization failed: %v", err)
	}

	if err := bot.registerCommand("poll", bot.handlePoll,
		"\"Question\" \"Option1\" \"Option2\"...",
		"Create a new poll"); err != nil {
		log.Fatalf("Failed to register command: %v", err)
	}

	if err := bot.registerCommand("vote", bot.handleVote,
		"<poll_id> <option_number>",
		"Vote in an existing poll"); err != nil {
		log.Fatalf("Failed to register command: %v", err)
	}

	if err := bot.registerCommand("get-poll", bot.handleGetPoll,
		"<poll_id>",
		"View poll results"); err != nil {
		log.Fatalf("Failed to register command: %v", err)
	}

	if err := bot.registerCommand("close-poll", bot.handleClosePoll,
		"<poll_id>",
		"Close a poll (creator only)"); err != nil {
		log.Fatalf("Failed to register command: %v", err)
	}

	if err := bot.registerCommand("delete-poll", bot.handleDeletePoll,
		"<poll_id>",
		"Delete a poll (creator only)"); err != nil {
		log.Fatalf("Failed to register command: %v", err)
	}

	if err := bot.registerCommand("vote-bot-help", bot.handleHelp,
		"",
		"Show help for Vote Bot commands"); err != nil {
		log.Fatalf("Failed to register command: %v", err)
	}

	bot.startCommandServer()
}

func (b *Bot) initialize() error {
	log.Println("Initializing Mattermost client...")
	b.client.SetToken(os.Getenv("MM_TOKEN"))

	log.Println("Connecting to Tarantool...")
	var conn *tarantool.Connection
	var err error
	for i := 0; i < 5; i++ {
		conn, err = tarantool.Connect(os.Getenv("TARANTOOL_ADDR"), b.tarantoolOpts)
		if err == nil {
			break
		}
		log.Printf("Connection attempt %d failed: %v", i+1, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to Tarantool after retries: %v", err)
	}

	_, err = conn.Ping()
	if err != nil {
		return fmt.Errorf("failed to ping Tarantool: %v", err)
	}

	b.tarantool = conn
	log.Println("Successfully connected to Tarantool")

	log.Println("Authenticating with Mattermost...")
	user, _, err := b.client.GetUserByUsername(os.Getenv("MM_USERNAME"), "")
	if err != nil {
		return fmt.Errorf("failed to get bot user: %v", err)
	}

	b.user = user
	log.Printf("Authenticated as user: %s (%s)", user.Username, user.Id)

	log.Println("Fetching team information...")
	team, _, err := b.client.GetTeamByName(os.Getenv("MM_TEAM"), "")
	if err != nil {
		return fmt.Errorf("failed to get team: %v", err)
	}

	b.team = team
	log.Printf("Found team: %s (%s)", team.Name, team.Id)

	return nil
}

func (b *Bot) registerCommand(trigger string, handler CommandHandler, hintAndDesc ...string) error {
	log.Printf("Registering command: /%s", trigger)
	b.commands[trigger] = handler

	autoCompleteHint := fmt.Sprintf("[args] for /%s", trigger)
	autoCompleteDesc := fmt.Sprintf("Execute %s command", trigger)

	if len(hintAndDesc) >= 1 {
		autoCompleteHint = hintAndDesc[0]
	}
	if len(hintAndDesc) >= 2 {
		autoCompleteDesc = hintAndDesc[1]
	}

	command := &model.Command{
		TeamId:           b.team.Id,
		Trigger:          trigger,
		Method:           "P",
		AutoComplete:     true,
		AutoCompleteHint: autoCompleteHint,
		AutoCompleteDesc: autoCompleteDesc,
		DisplayName:      fmt.Sprintf("%s Command", strings.Title(trigger)),
		Description:      fmt.Sprintf("Execute %s command", trigger),
		URL:              fmt.Sprintf("%s/commands", getServerBaseURL()),
		CreatorId:        b.user.Id,
	}

	existingCommands, _, err := b.client.ListCommands(b.team.Id, false)
	if err != nil {
		return fmt.Errorf("failed to list existing commands: %v", err)
	}

	var existingCmd *model.Command
	for _, cmd := range existingCommands {
		if cmd.Trigger == trigger {
			existingCmd = cmd
			break
		}
	}

	if existingCmd != nil {
		command.Id = existingCmd.Id
		_, _, err = b.client.UpdateCommand(command)
		if err != nil {
			return fmt.Errorf("failed to update existing command: %v", err)
		}
		log.Printf("Command /%s updated successfully", trigger)
	} else {
		_, _, err := b.client.CreateCommand(command)
		if err != nil {
			return fmt.Errorf("failed to register new command in Mattermost: %v", err)
		}
		log.Printf("Command /%s created successfully", trigger)
	}

	return nil
}

func (b *Bot) startCommandServer() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8088"
	}

	http.HandleFunc("/commands", b.handleCommandRequest)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("Starting command server on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func (b *Bot) handleCommandRequest(w http.ResponseWriter, r *http.Request) {
	log.Printf("Incoming %s request from %s", r.Method, r.RemoteAddr)

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType != "application/x-www-form-urlencoded" {
		log.Printf("Unexpected Content-Type: %s", contentType)
		http.Error(w, "Content-Type must be application/x-www-form-urlencoded", http.StatusUnsupportedMediaType)
		return
	}

	if err := r.ParseForm(); err != nil {
		log.Printf("Error parsing form: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	log.Printf("Form data: %+v", r.Form)

	payload := CommandRequest{
		Token:       r.Form.Get("token"),
		TeamID:      r.Form.Get("team_id"),
		TeamDomain:  r.Form.Get("team_domain"),
		ChannelID:   r.Form.Get("channel_id"),
		ChannelName: r.Form.Get("channel_name"),
		UserID:      r.Form.Get("user_id"),
		UserName:    r.Form.Get("user_name"),
		Command:     r.Form.Get("command"),
		Text:        r.Form.Get("text"),
		ResponseURL: r.Form.Get("response_url"),
		TriggerID:   r.Form.Get("trigger_id"),
	}

	trigger := strings.TrimPrefix(payload.Command, "/")
	handler, exists := b.commands[trigger]
	if !exists {
		http.Error(w, "Command not found", http.StatusNotFound)
		return
	}

	response, err := handler(payload)
	if err != nil {
		log.Printf("Handler error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"response_type": "in_channel",
		"text":          response,
	})
}

func (b *Bot) handleHelp(_ CommandRequest) (string, error) {
	return "🤖 *Poll Bot Help* 🤖\n\n" +
		"*Available Commands:*\n\n" +
		"1. */poll \"Question\" \"Option1\" \"Option2\" ...*\n" +
		"   _Create a new poll_\n" +
		"   Example: `/poll \"Favorite color?\" \"Red\" \"Blue\" \"Green\"`\n\n" +
		"2. */vote <poll_id> <option_number>*\n" +
		"   _Vote in an existing poll_\n" +
		"   Example: `/vote 42 2` (votes for option 2 in poll 42)\n\n" +
		"3. */get-poll <poll_id>*\n" +
		"   _View poll results_\n" +
		"   Example: `/get-poll 42`\n\n" +
		"4. */close-poll <poll_id>*\n" +
		"   _Close a poll (creator only)_\n" +
		"   Example: `/close-poll 42`\n\n" +
		"5. */delete-poll <poll_id>*\n" +
		"   _Delete a poll (creator only)_\n" +
		"   Example: `/delete-poll 42`\n\n" +
		"6. */vote-bot-help*\n" +
		"   _Show this help message_\n\n" +
		"*Notes:*\n" +
		"- Poll IDs are shown when you create a poll\n" +
		"- Only poll creators can close or delete polls\n" +
		"- Votes are channel-specific", nil
}

func (b *Bot) handlePoll(req CommandRequest) (string, error) {
	log.Printf("Processing poll command from user %s in channel %s", req.UserID, req.ChannelID)

	question, options := parsePoll(req.Text)
	if question == "" {
		return "❌ Poll question cannot be empty. Usage: /poll \"Question\" \"Option1\" \"Option2\" ...", nil
	}

	if options == nil {
		return "❌ Options required", nil
	}

	resp, err := b.tarantool.Call("create_poll", []interface{}{
		req.UserID,
		req.ChannelID,
		question,
		options,
	})

	if err != nil {
		return "", fmt.Errorf("Tarantool error: %v", err)
	}

	if len(resp.Data) >= 2 && resp.Data[1] != nil {
		return "", fmt.Errorf("%v", resp.Data[1])
	}

	var pollID uint64
	switch v := resp.Data[0].(type) {
	case []interface{}:
		pollID = v[0].(uint64)
	}

	response := fmt.Sprintf(
		"🗳️ *Poll Created* (ID: `%d`)\n\n"+
			"**`%s`**\n%s\n",
		pollID,
		question,
		formatOptions(options),
	)

	return response, nil
}

func (b *Bot) handleVote(req CommandRequest) (string, error) {
	log.Printf("Processing vote command from user %s in channel %s", req.UserID, req.ChannelID)
	pollID, optionIndex, err := parseVote(req.Text)
	if err != nil {
		return "❌ " + err.Error(), nil
	}

	log.Printf("data: %d %d", pollID, optionIndex)

	resp, err := b.tarantool.Call("vote", []interface{}{
		pollID,
		req.UserID,
		optionIndex,
		req.ChannelID,
	})

	if err != nil {
		return "", fmt.Errorf("Tarantool error: %v", err)
	}

	if len(resp.Data) >= 2 && resp.Data[1] != nil {
		var respErr string
		switch v := resp.Data[1].(type) {
		case []interface{}:
			respErr = v[0].(string)
		}

		return fmt.Sprintf("❌ %v", respErr), nil
	}

	return fmt.Sprintf("Successfully voted for option %d in the poll %d",
		optionIndex,
		pollID), nil
}

func (b *Bot) handleGetPoll(req CommandRequest) (string, error) {
	log.Println("Handling get-poll command")

	pollID, err := strconv.ParseUint(req.Text, 10, 64)
	if err != nil {
		return "❌ Invalid poll ID format", nil
	}

	resp, err := b.tarantool.Call("get_poll", []interface{}{pollID, req.ChannelID})
	if err != nil {
		log.Printf("Tarantool call error: %v", err)
		return "", fmt.Errorf("Tarantool error: %v", err)
	}

	if len(resp.Data) >= 2 && resp.Data[1] != nil {
		var respErr string
		switch v := resp.Data[1].(type) {
		case []interface{}:
			respErr = v[0].(string)
		}

		return fmt.Sprintf("❌ %v", respErr), nil
	}

	pollData, ok := resp.Data[0].([]interface{})
	if !ok || len(pollData) < 10 {
		return "", fmt.Errorf("Invalid poll data format")
	}

	poll := struct {
		ID       uint64
		Question string
		Options  []string
		Votes    map[string]int
		Active   bool
	}{}

	switch v := pollData[0].(type) {
	case uint64:
		poll.ID = v
	}

	if question, ok := pollData[1].(string); ok {
		poll.Question = question
	} else {
		return "", fmt.Errorf("invalid question type")
	}

	if active, ok := pollData[7].(bool); ok {
		poll.Active = active
	} else {
		return "", fmt.Errorf("invalid active status type")
	}

	options, ok := pollData[2].([]interface{})
	if !ok {
		return "", fmt.Errorf("invalid options type")
	}

	poll.Options = make([]string, len(options))
	for i, opt := range options {
		if s, ok := opt.(string); ok {
			poll.Options[i] = s
		} else {
			return "", fmt.Errorf("invalid option type at index %d", i)
		}
	}

	votes, ok := pollData[3].(map[interface{}]interface{})
	if !ok {
		return "", fmt.Errorf("invalid votes type")
	}

	poll.Votes = make(map[string]int)
	for k, v := range votes {
		key, ok1 := k.(string)
		val, ok2 := v.(uint64)
		if !ok1 || !ok2 {
			return "", fmt.Errorf("invalid vote data")
		}
		poll.Votes[key] = int(val)
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("📊 Poll (ID `%d`)\n", poll.ID))
	builder.WriteString(fmt.Sprintf("%s\n", map[bool]string{true: "✅ Active", false: "❌ Closed"}[poll.Active]))
	builder.WriteString("\n")
	builder.WriteString(fmt.Sprintf("`%s`\n", poll.Question))

	for i, option := range poll.Options {
		votes := poll.Votes[fmt.Sprint(i+1)]
		builder.WriteString(fmt.Sprintf("%d. `%s` - %d votes\n", i+1, option, votes))
	}

	return builder.String(), nil
}

func (b *Bot) handleClosePoll(req CommandRequest) (string, error) {
	log.Println("Handling close-poll command")

	pollID, err := strconv.ParseUint(req.Text, 10, 64)
	if err != nil {
		return "❌ Invalid poll ID format", nil
	}

	resp, err := b.tarantool.Call("close_poll", []interface{}{pollID, req.UserID, req.ChannelID})
	if err != nil {
		return "", fmt.Errorf("Tarantool error: %v", err)
	}

	if len(resp.Data) >= 2 && resp.Data[1] != nil {
		var respErr string
		switch v := resp.Data[1].(type) {
		case []interface{}:
			respErr = v[0].(string)
		}

		return fmt.Sprintf("❌ %v", respErr), nil
	}

	return fmt.Sprintf("Poll (ID `%d`) closed", pollID), nil
}

func (b *Bot) handleDeletePoll(req CommandRequest) (string, error) {
	log.Println("Handling delete-poll command")

	pollID, err := strconv.ParseUint(req.Text, 10, 64)
	if err != nil {
		return "❌ Invalid poll ID format", nil
	}

	resp, err := b.tarantool.Call("delete_poll", []interface{}{pollID, req.UserID, req.ChannelID})
	if err != nil {
		return "", fmt.Errorf("Tarantool error: %v", err)
	}

	if len(resp.Data) >= 2 && resp.Data[1] != nil {
		var respErr string
		switch v := resp.Data[1].(type) {
		case []interface{}:
			respErr = v[0].(string)
		}

		return fmt.Sprintf("❌ %v", respErr), nil
	}

	return fmt.Sprintf("Poll (ID `%d`) deleted", pollID), nil
}

func parseVote(text string) (uint64, int, error) {
	parsed := strings.Split(strings.TrimSpace(text), " ")

	if len(parsed) != 2 {
		return 0, -1, fmt.Errorf("invalid input format")
	}

	pollID, err := strconv.ParseUint(parsed[0], 10, 64)
	if err != nil {
		return 0, -1, fmt.Errorf("invalid poll ID")
	}

	optionIndex, err := strconv.Atoi(parsed[1])
	if err != nil {
		return 0, -1, fmt.Errorf("invalid option index")
	}

	return pollID, optionIndex, nil
}

func formatOptions(options []string) string {
	var builder strings.Builder
	for i, opt := range options {
		builder.WriteString(fmt.Sprintf("%d. `%s`\n", i+1, opt))
	}
	return builder.String()
}

func getServerBaseURL() string {
	host := os.Getenv("HOST")
	if host == "" {
		host = "host.docker.internal"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8088"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

func parsePoll(text string) (string, []string) {
	var question string
	var options []string
	var index = 0

	for index != -1 {
		if index == 0 {
			question, index = parseToken(index, text)
		} else {
			var option string
			option, index = parseToken(index, text)
			if index == -1 {
				break
			}

			options = append(options, option)
		}
	}

	return question, options
}

func parseToken(index int, text string) (token string, newIndex int) {
	for index < len(text) && text[index] == ' ' {
		index++
	}

	index++
	start := index

	for index < len(text) && text[index] != '"' {
		index++
	}

	if index >= len(text) {
		return "", -1
	}

	return text[start:index], index + 1
}
