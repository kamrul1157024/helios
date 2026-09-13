package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/daemon"
	"github.com/kamrul1157024/helios/internal/terminal"
)

/*
`helios chat` is how an agent takes part in a channel. See
docs/specs/60-group-chat.md.

The desktop app has a sidebar for this; a session has a shell. Everything the
joining prompt promises a member it can do lives here, and nothing else does —
an agent that cannot run these commands is in a conversation it can only be
shouted at from.

Every command signs itself with the session it is running in, read from the
environment the terminal host exports. That is how the daemon knows whose
receipt to move and who to leave out of the fan-out: a session must not be sent
its own message back as a prompt.
*/

const chatUsage = `Usage: helios chat <command>

  list                                  the channels, with what you have not read
  sessions                              who could be invited: id, title, cwd, status
  new [name] --with <id,id>             start one, optionally with a first message
  post <channel> "<message>"            say something to everyone in it
  read <channel> [--since-last]         the conversation, and marks it read
  join <channel> --session <id>         add a session to it
  leave <channel> --session <id>        take one out
  archive <channel>                     close it: readable, but it takes no more
  unarchive <channel>                   reopen it
  delete <channel>                      remove it and everything said in it

Flags:
  --with <id,id>      members for a new channel
  --message "<text>"  a first message to open a new channel with
  --session <id>      act as, or act on, this session instead of the one you are in
  --since-last        only what has arrived since you last read
  --urgent            interrupt the members rather than queue behind their work
  --archived          include the closed channels in the list
  --json              machine-readable output

A channel is named by its name where it has one, and by its id where it does
not. ` + "`helios chat list`" + ` prints whichever will work.

general is the notice board: every session on this daemon is in it, and a post
there interrupts nobody. Read it at the start of a piece of work and before
anything wide, and post there before doing something wide yourself.
`

type wireChannel struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Members  []string          `json:"members"`
	Titles   map[string]string `json:"titles"`
	Unread   int               `json:"unread"`
	Archived bool              `json:"archived"`
}

type wireMessage struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	From      string `json:"from"`
	Body      string `json:"body"`
	Urgent    bool   `json:"urgent"`
	CreatedAt string `json:"created_at"`
}

func handleChat(args []string) {
	if len(args) == 0 {
		fmt.Print(chatUsage)
		return
	}

	switch args[0] {
	case "list", "ls":
		chatList(args[1:])
	case "sessions":
		chatSessions(args[1:])
	case "new":
		chatNew(args[1:])
	case "post", "say":
		chatPost(args[1:])
	case "read":
		chatRead(args[1:])
	case "join":
		chatMember(args[1:], http.MethodPost)
	case "leave":
		chatMember(args[1:], http.MethodDelete)
	case "archive":
		chatArchive(args[1:], true)
	case "unarchive":
		chatArchive(args[1:], false)
	case "delete", "rm":
		chatDelete(args[1:])
	case "help", "--help", "-h":
		fmt.Print(chatUsage)
	default:
		fmt.Fprintf(os.Stderr, "Unknown: helios chat %s\n\n", args[0])
		fmt.Fprint(os.Stderr, chatUsage)
		os.Exit(1)
	}
}

// chatSelf is the session this command is running inside, which the terminal
// host exports into every PTY it owns. Empty when a person runs it from their
// own shell, and that is the difference the daemon acts on: a person is sent
// nothing, because they are reading the reply already.
func chatSelf() string {
	return os.Getenv(terminal.SessionEnv)
}

func chatURL(path string, query url.Values) string {
	cfg, _ := daemon.LoadConfig()
	out := fmt.Sprintf("http://127.0.0.1:%d/internal/channels%s", cfg.Server.InternalPort, path)
	if len(query) > 0 {
		out += "?" + query.Encode()
	}
	return out
}

func callChat(method, path string, query url.Values, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, chatURL(path, query), reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("helios is not running? %w", err)
	}
	defer resp.Body.Close()

	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode >= 400 {
		message, _ := out["message"].(string)
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("%s", message)
	}
	return out, nil
}

func chatFail(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

// chatFlags reads the flags every subcommand shares, and returns what was left
// over — the channel and the message, which are positional because that is how
// they read: `helios chat post api-redesign "…"`.
type chatOpts struct {
	with      []string
	message   string
	session   string
	sinceLast bool
	urgent    bool
	archived  bool
	asJSON    bool
	rest      []string
}

func chatFlags(args []string) chatOpts {
	opts := chatOpts{}
	for at := 0; at < len(args); at++ {
		next := func() string {
			if at+1 < len(args) {
				at++
				return args[at]
			}
			chatFail(fmt.Errorf("%s wants a value", args[at]))
			return ""
		}
		switch args[at] {
		case "--with":
			for _, one := range strings.Split(next(), ",") {
				if one = strings.TrimSpace(one); one != "" {
					opts.with = append(opts.with, one)
				}
			}
		case "--message", "-m":
			opts.message = next()
		case "--session":
			opts.session = next()
		case "--since-last":
			opts.sinceLast = true
		case "--urgent":
			opts.urgent = true
		case "--archived":
			opts.archived = true
		case "--json":
			opts.asJSON = true
		default:
			opts.rest = append(opts.rest, args[at])
		}
	}
	return opts
}

// reader is whose unread count and receipt a command is about: the session
// named on the command line, else the one it is running in.
func (o chatOpts) reader() url.Values {
	who := o.session
	if who == "" {
		who = chatSelf()
	}
	if who == "" {
		return nil
	}
	return url.Values{"session": {who}}
}

func fetchChannels(query url.Values) ([]wireChannel, error) {
	out, err := callChat(http.MethodGet, "", query, nil)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(out["channels"])
	var channels []wireChannel
	json.Unmarshal(raw, &channels)
	return channels, nil
}

// findChannel resolves what somebody typed. A name is what they will have read
// off a prompt, and an id is what an unnamed channel has instead.
//
// Closed channels are included: `unarchive` has to be able to find the channel
// it exists to reopen, and a refusal saying why beats "no channel called that".
func findChannel(name string) (*wireChannel, error) {
	if name == "" {
		return nil, fmt.Errorf("which channel? try: helios chat list")
	}
	channels, err := fetchChannels(url.Values{"archived": {"1"}})
	if err != nil {
		return nil, err
	}
	for at := range channels {
		if channels[at].Name == name || channels[at].ID == name {
			return &channels[at], nil
		}
	}
	return nil, fmt.Errorf("no channel called %q. Try: helios chat list", name)
}

func chatLabel(ch wireChannel) string {
	if ch.Name != "" {
		return ch.Name
	}
	return ch.ID
}

func chatList(args []string) {
	opts := chatFlags(args)
	query := opts.reader()
	if opts.archived {
		if query == nil {
			query = url.Values{}
		}
		query.Set("archived", "1")
	}

	channels, err := fetchChannels(query)
	if err != nil {
		chatFail(err)
	}
	if opts.asJSON {
		printJSON(map[string]any{"channels": channels})
		return
	}
	if len(channels) == 0 {
		fmt.Println("No channels. Try: helios chat new --with <session-id,session-id>")
		return
	}

	fmt.Printf("%-24s %-7s %s\n", "Channel", "Unread", "Members")
	fmt.Println(strings.Repeat("-", 80))
	for _, ch := range channels {
		unread := ""
		if ch.Unread > 0 {
			unread = fmt.Sprintf("%d", ch.Unread)
		}
		if ch.Archived {
			unread = "closed"
		}
		who := make([]string, 0, len(ch.Members))
		for _, member := range ch.Members {
			if title := ch.Titles[member]; title != "" {
				who = append(who, title)
			} else {
				who = append(who, member)
			}
		}
		fmt.Printf("%-24s %-7s %s\n", chatLabel(ch), unread, strings.Join(who, ", "))
	}
}

// chatSessions is how an agent finds the others. A session knows its own id
// from the environment; everything else it has to ask for.
func chatSessions(args []string) {
	opts := chatFlags(args)
	cfg, _ := daemon.LoadConfig()

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(
		fmt.Sprintf("http://127.0.0.1:%d/internal/sessions", cfg.Server.InternalPort))
	if err != nil {
		chatFail(fmt.Errorf("helios is not running? %w", err))
	}
	defer resp.Body.Close()

	var result struct {
		Sessions []struct {
			SessionID string  `json:"session_id"`
			Title     *string `json:"title"`
			CWD       string  `json:"cwd"`
			Status    string  `json:"status"`
		} `json:"sessions"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if opts.asJSON {
		printJSON(result)
		return
	}
	if len(result.Sessions) == 0 {
		fmt.Println("No sessions.")
		return
	}

	self := chatSelf()
	marked := false
	fmt.Printf("%-14s %-40s %-10s %s\n", "Session", "Title", "Status", "CWD")
	fmt.Println(strings.Repeat("-", 100))
	for _, s := range result.Sessions {
		title := ""
		if s.Title != nil {
			title = *s.Title
		}
		id := s.SessionID
		if id == self {
			id += " *"
			marked = true
		}
		fmt.Printf("%-14s %-40s %-10s %s\n", id, truncateTo(title, 40), s.Status, s.CWD)
	}
	if marked {
		fmt.Println("\n* you")
	}
}

func chatNew(args []string) {
	opts := chatFlags(args)
	if len(opts.with) == 0 {
		chatFail(fmt.Errorf("who is in it? --with <session-id,session-id>"))
	}

	name := ""
	if len(opts.rest) > 0 {
		name = opts.rest[0]
	}
	// `helios chat new "api-redesign" --with … "the first thing"` reads better
	// than making the opener a flag, so a second positional is the message.
	message := opts.message
	if message == "" && len(opts.rest) > 1 {
		message = opts.rest[1]
	}

	out, err := callChat(http.MethodPost, "", nil, map[string]any{
		"name": name, "members": opts.with, "message": message,
		"author": chatAuthor(""), "urgent": opts.urgent,
	})
	if err != nil {
		chatFail(err)
	}
	if opts.asJSON {
		printJSON(out)
		return
	}

	channel, _ := out["channel"].(map[string]any)
	id, _ := channel["id"].(string)
	label, _ := channel["name"].(string)
	if label == "" {
		label = id
	}
	if existing, _ := out["existing"].(bool); existing {
		fmt.Printf("Those sessions already have a channel: %s\n", label)
		return
	}
	fmt.Printf("Channel %s. The members have been told they are in it.\n", label)
}

// chatAuthor is who a message is from in the daemon's terms. A session signs
// with its own id; a person running this from their own shell signs as nobody
// and the daemon files it as the user.
func chatAuthor(session string) string {
	if session == "" {
		session = chatSelf()
	}
	if session == "" {
		return ""
	}
	return "session:" + session
}

func chatPost(args []string) {
	opts := chatFlags(args)
	if len(opts.rest) < 2 && opts.message == "" {
		chatFail(fmt.Errorf(`usage: helios chat post <channel> "<message>"`))
	}
	name := ""
	if len(opts.rest) > 0 {
		name = opts.rest[0]
	}
	message := opts.message
	if message == "" {
		message = strings.Join(opts.rest[1:], " ")
	}

	ch, err := findChannel(name)
	if err != nil {
		chatFail(err)
	}
	out, err := callChat(http.MethodPost, "/"+ch.ID+"/messages", nil, map[string]any{
		"message": message, "author": chatAuthor(opts.session), "urgent": opts.urgent,
	})
	if err != nil {
		chatFail(err)
	}
	if opts.asJSON {
		printJSON(out)
		return
	}
	fmt.Printf("Posted to %s.\n", chatLabel(*ch))
}

func chatRead(args []string) {
	opts := chatFlags(args)
	name := ""
	if len(opts.rest) > 0 {
		name = opts.rest[0]
	}
	ch, err := findChannel(name)
	if err != nil {
		chatFail(err)
	}

	query := opts.reader()
	if query == nil {
		query = url.Values{}
	}
	if opts.sinceLast {
		query.Set("since_last", "1")
	}

	out, err := callChat(http.MethodGet, "/"+ch.ID+"/messages", query, nil)
	if err != nil {
		chatFail(err)
	}
	raw, _ := json.Marshal(out["messages"])
	var messages []wireMessage
	json.Unmarshal(raw, &messages)

	if opts.asJSON {
		printJSON(map[string]any{"messages": messages})
		return
	}
	if len(messages) == 0 {
		if opts.sinceLast {
			fmt.Printf("Nothing new in %s.\n", chatLabel(*ch))
			return
		}
		fmt.Printf("Nothing said in %s yet.\n", chatLabel(*ch))
		return
	}

	for _, m := range messages {
		mark := ""
		if m.Urgent {
			mark = " (urgent)"
		}
		fmt.Printf("%s %s%s\n%s\n\n", chatWhen(m.CreatedAt), m.From, mark, m.Body)
	}
}

func chatMember(args []string, method string) {
	opts := chatFlags(args)
	if opts.session == "" {
		chatFail(fmt.Errorf("which session? --session <id>"))
	}
	name := ""
	if len(opts.rest) > 0 {
		name = opts.rest[0]
	}
	ch, err := findChannel(name)
	if err != nil {
		chatFail(err)
	}

	if method == http.MethodDelete {
		if _, err := callChat(method, "/"+ch.ID+"/members/"+opts.session, nil, nil); err != nil {
			chatFail(err)
		}
		fmt.Printf("%s is out of %s.\n", opts.session, chatLabel(*ch))
		return
	}

	out, err := callChat(method, "/"+ch.ID+"/members", nil, map[string]any{"session": opts.session})
	if err != nil {
		chatFail(err)
	}
	if added, _ := out["added"].(bool); !added {
		fmt.Printf("%s was already in %s.\n", opts.session, chatLabel(*ch))
		return
	}
	fmt.Printf("%s is in %s, and has been told so.\n", opts.session, chatLabel(*ch))
}

// chatArchive closes a channel, or reopens it. Closing is not deleting: what
// was said stays readable, and only the next message is refused.
func chatArchive(args []string, archived bool) {
	opts := chatFlags(args)
	name := ""
	if len(opts.rest) > 0 {
		name = opts.rest[0]
	}
	ch, err := findChannel(name)
	if err != nil {
		chatFail(err)
	}
	if _, err := callChat(http.MethodPost, "/"+ch.ID+"/archive", nil,
		map[string]any{"archived": archived}); err != nil {
		chatFail(err)
	}
	if archived {
		fmt.Printf("%s is closed. It still reads, and it takes no more messages.\n", chatLabel(*ch))
		return
	}
	fmt.Printf("%s is open again.\n", chatLabel(*ch))
}

func chatDelete(args []string) {
	opts := chatFlags(args)
	name := ""
	if len(opts.rest) > 0 {
		name = opts.rest[0]
	}
	ch, err := findChannel(name)
	if err != nil {
		chatFail(err)
	}
	if _, err := callChat(http.MethodDelete, "/"+ch.ID, nil, nil); err != nil {
		chatFail(err)
	}
	fmt.Printf("%s is gone, and everything said in it.\n", chatLabel(*ch))
}

func chatWhen(iso string) string {
	at, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	return at.Local().Format("15:04")
}

func truncateTo(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit-1] + "…"
}

func printJSON(value any) {
	raw, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(raw))
}
