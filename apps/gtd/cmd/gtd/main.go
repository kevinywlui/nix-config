// Command gtd is a thin client for gtd-server's JSON API. It works identically
// on the t480 (GTD_ENDPOINT defaults to localhost) and from fw13 over Tailscale
// (set GTD_ENDPOINT to the tailnet URL).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type task struct {
	ID       int      `json:"id"`
	Text     string   `json:"text"`
	Contexts []string `json:"contexts"`
	Due      string   `json:"due"`
}

func endpoint() string {
	if e := os.Getenv("GTD_ENDPOINT"); e != "" {
		return strings.TrimRight(e, "/")
	}
	return "http://127.0.0.1:8730"
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "add", "capture":
		err = add(strings.Join(args, " "))
	case "inbox":
		err = list("inbox", "")
	case "next":
		ctx := ""
		if len(args) > 0 {
			ctx = args[0]
		}
		err = list("next", ctx)
	case "waiting":
		err = list("waiting", "")
	case "ls", "all":
		err = list("all", "")
	case "done":
		if len(args) != 1 {
			err = fmt.Errorf("usage: gtd done <id>")
			break
		}
		err = done(args[0])
	case "edit":
		if len(args) < 2 {
			err = fmt.Errorf("usage: gtd edit <id> <text...>")
			break
		}
		err = edit(args[0], strings.Join(args[1:], " "))
	case "undo":
		err = undo()
	case "log", "done-list":
		err = list("done", "")
	case "restore":
		if len(args) != 1 {
			err = fmt.Errorf("usage: gtd restore <id>")
			break
		}
		err = restore(args[0])
	case "-h", "--help", "help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gtd:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gtd — guided GTD over todo.txt

usage:
  gtd add <text...>     capture a thought into your inbox
  gtd inbox             list unprocessed items
  gtd next [context]    list next actions, optionally by @context
  gtd waiting           list delegated / waiting-for items
  gtd ls                list all active tasks
  gtd done <id>         complete the task with that id
  gtd edit <id> <text>  replace the wording of the task with that id
  gtd undo              roll back the last change
  gtd log               list completed tasks
  gtd restore <id>      bring a completed task back to your active list

GTD_ENDPOINT selects the server (default http://127.0.0.1:8730).
`)
}

func request(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, endpoint()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-GTD-Client", "cli")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

func add(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("nothing to capture")
	}
	payload, _ := json.Marshal(map[string]string{"text": text})
	resp, err := request(http.MethodPost, "/api/capture", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return httpErr(resp)
	}
	fmt.Println("captured.")
	return nil
}

func list(view, context string) error {
	q := url.Values{"view": {view}}
	if context != "" {
		q.Set("context", context)
	}
	resp, err := request(http.MethodGet, "/api/tasks?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpErr(resp)
	}
	var tasks []task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Println("(nothing)")
		return nil
	}
	for _, t := range tasks {
		line := fmt.Sprintf("%3d  %s", t.ID, t.Text)
		if t.Due != "" {
			line += "  (due " + t.Due + ")"
		}
		fmt.Println(line)
	}
	return nil
}

func done(idStr string) error {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return fmt.Errorf("id must be a number")
	}
	payload, _ := json.Marshal(map[string]int{"id": id})
	resp, err := request(http.MethodPost, "/api/done", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return httpErr(resp)
	}
	fmt.Println("done.")
	return nil
}

func edit(idStr, text string) error {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return fmt.Errorf("id must be a number")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("nothing to set")
	}
	payload, _ := json.Marshal(map[string]any{"id": id, "text": text})
	resp, err := request(http.MethodPost, "/api/edit", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return httpErr(resp)
	}
	fmt.Println("edited.")
	return nil
}

func undo() error {
	resp, err := request(http.MethodPost, "/api/undo", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return httpErr(resp)
	}
	fmt.Println("undone.")
	return nil
}

func restore(idStr string) error {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return fmt.Errorf("id must be a number")
	}
	payload, _ := json.Marshal(map[string]int{"id": id})
	resp, err := request(http.MethodPost, "/api/restore", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return httpErr(resp)
	}
	fmt.Println("restored.")
	return nil
}

func httpErr(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(b))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("server returned %d: %s", resp.StatusCode, msg)
}
