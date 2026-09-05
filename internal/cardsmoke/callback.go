package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/marcelritzschke/claude-code-feishu-companion/internal/config"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/feishu"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/mcp"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/notify"
	"github.com/marcelritzschke/claude-code-feishu-companion/internal/session"
)

// checkCallback puts one card with buttons up and waits for the user to
// tap one, reporting exactly what came back.
//
// This is the one thing about schema 2.0 that cannot be checked without a
// person: v1 carried a button's payload in the action element's value,
// and 2.0 carries it in behaviors[].value. Whether the SDK still surfaces
// it at event.Event.Action.Value is what a tap decides.
func checkCallback(cfg *config.Config, c *feishu.Client) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in := feishu.NewInbound(cfg)
	go func() {
		if err := in.Run(ctx); err != nil && ctx.Err() == nil {
			fmt.Println("inbound stopped:", err)
		}
	}()
	// The websocket takes a moment to come up; a tap before it does would
	// be delivered to nobody.
	time.Sleep(3 * time.Second)

	// A permission card is the right probe: its two buttons are
	// unconditional, and two buttons is the harder layout - 2.0 has no
	// action container, so a row is a column_set with the buttons nested
	// inside columns. If a payload survives that nesting it survives the
	// lone-button case too.
	s := session.Session{
		ID: "callback-probe", Dir: "/tmp/callback-probe", Title: "Callback round-trip probe",
		State: session.Waiting, Remote: session.Ready,
	}
	req := mcp.PermissionRequest{
		RequestID: "probe", ToolName: "Bash",
		Description:  "Nothing is run either way - this only checks that the tap gets back here",
		InputPreview: "echo 'callback probe'",
	}
	card, err := notify.PermissionRelayCard(s, req)
	if err != nil {
		fmt.Println("build:", err)
		return 1
	}
	if got := buttonCount(card); got != 2 {
		fmt.Printf("the probe card carries %d buttons, so a tap cannot be tested\n", got)
		return 1
	}
	id, err := c.SendCard(ctx, card)
	if err != nil {
		fmt.Println("send:", err)
		return 1
	}
	defer func() { _ = c.DeleteMessage(context.Background(), id) }()

	fmt.Println("A card is now in your Feishu DM. Tap any button on it.")
	fmt.Println("Waiting up to 2 minutes... (Ctrl-C to give up)")

	select {
	case act := <-in.Actions():
		var pretty any
		_ = json.Unmarshal(act.Value, &pretty)
		raw, _ := json.Marshal(pretty)
		fmt.Printf("\nreceived: %s\n", raw)
		got, ok := notify.ParseAction(act.Value)
		if !ok {
			fmt.Println("FAIL  the payload did not parse as an Action.")
			fmt.Println("      2.0 puts the value in behaviors[].value; the SDK is")
			fmt.Println("      evidently not surfacing it at Action.Value any more.")
			return 1
		}
		if got.Kind != notify.ActionPermit || got.Request != req.RequestID ||
			(got.Verdict != notify.VerdictAllow && got.Verdict != notify.VerdictDeny) {
			fmt.Printf("FAIL  parsed %+v, want a permit verdict for request %q\n", got, req.RequestID)
			return 1
		}
		fmt.Printf("      you tapped %q\n", got.Verdict)
		if act.MessageID != id {
			fmt.Printf("WARN  callback names message %s, card is %s - settling in place would miss.\n",
				act.MessageID, id)
			return 1
		}
		fmt.Println("PASS  the button payload survives schema 2.0 unchanged,")
		fmt.Println("      and the callback names the card it came from.")
		return 0
	case <-time.After(2 * time.Minute):
		fmt.Println("\nTIMEOUT  nothing arrived.")
		fmt.Println("  Either the tap did not reach us, or this app has no card")
		fmt.Println("  callback subscription. Check that the Feishu app subscribes to")
		fmt.Println("  card callbacks over long connection, and that no other")
		fmt.Println("  claude-companion daemon is holding the websocket - two")
		fmt.Println("  connections for one app compete for the same events.")
		return 1
	}
}

// callbackMode reports whether the harness was asked to check the callback
// round trip instead of rendering every card.
func callbackMode() bool {
	return len(os.Args) > 1 && os.Args[1] == "-callback"
}

// buttonCount reports how many buttons a rendered card carries, so the
// probe fails loudly rather than asking for a tap on a card that has
// nothing to tap.
func buttonCount(cardJSON string) int {
	var m map[string]any
	if json.Unmarshal([]byte(cardJSON), &m) != nil {
		return 0
	}
	var walk func(v any) int
	walk = func(v any) int {
		n := 0
		switch t := v.(type) {
		case map[string]any:
			if t["tag"] == "button" {
				n++
			}
			for _, inner := range t {
				n += walk(inner)
			}
		case []any:
			for _, inner := range t {
				n += walk(inner)
			}
		}
		return n
	}
	return walk(m)
}
