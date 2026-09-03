package register

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
)

// TestExplainIsActionable checks the property every one of these messages
// has to have: a user who reads it knows what to do next. A bare error
// code from a device-authorization flow tells them nothing.
func TestExplainIsActionable(t *testing.T) {
	regErr := &registration.RegisterAppError{Code: "invalid_request", Description: "bad addons"}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"declined", &registration.AccessDeniedError{RegisterAppError: regErr}, "administrator"},
		{"expired", &registration.ExpiredError{RegisterAppError: regErr}, "claude-companion init"},
		{"timed out", context.DeadlineExceeded, "claude-companion init"},
		{"refused", regErr, "bad addons"},
		{"anything else", errors.New("dial tcp: no route to host"), "no route to host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := explain(tc.err)
			if got == nil {
				t.Fatal("explain returned nil for a failure")
			}
			if !strings.Contains(got.Error(), tc.want) {
				t.Errorf("explain(%v) = %q, want it to mention %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestQuietlyDropsOnlyTheSDKChatter guards both halves of the workaround:
// the stray line goes, and anything else the SDK might one day print still
// reaches the user.
func TestQuietlyDropsOnlyTheSDKChatter(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()

	inner := quietly(func() error {
		fmt.Println(sdkChatter + "feishu")
		fmt.Println("something worth seeing")
		return errors.New("carried out")
	})
	if inner == nil || inner.Error() != "carried out" {
		t.Errorf("quietly returned %v, want the inner error", inner)
	}

	os.Stdout = saved
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, sdkChatter) {
		t.Errorf("the SDK chatter reached the user: %q", got)
	}
	if !strings.Contains(got, "something worth seeing") {
		t.Errorf("real output was swallowed: %q", got)
	}
}

// TestNeedsCoversCompanionsTwoDirections is a reminder in test form: the
// list is what a user's app will be able to do, so shrinking it silently
// breaks a feature and growing it silently asks for more than Claude Companion
// uses.
func TestNeedsCoversCompanionsTwoDirections(t *testing.T) {
	needs := Needs()
	for _, want := range []struct {
		what string
		list []string
		item string
	}{
		{"sending cards", needs.Scopes, "im:message:send_as_bot"},
		{"reading replies", needs.Events, "im.message.receive_v1"},
		{"card buttons", needs.Callbacks, "card.action.trigger"},
	} {
		if !contains(want.list, want.item) {
			t.Errorf("%s needs %q, which Needs() no longer asks for", want.what, want.item)
		}
	}
}

func TestExpiryFallsBackWhenFeishuIsSilent(t *testing.T) {
	if got := expiry(600); got != 10*time.Minute {
		t.Errorf("expiry(600) = %v, want 10m", got)
	}
	// A missing or nonsensical lifetime must still produce a countdown
	// the front end can show, not a zero that reads as "already expired".
	for _, seconds := range []int{0, -1} {
		if got := expiry(seconds); got <= 0 {
			t.Errorf("expiry(%d) = %v, want a positive fallback", seconds, got)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
