// Package feishu delivers interactive cards to the user's Feishu DM as the
// bot, via the official oapi-sdk-go.
package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/marcelritzschke/wirelark/internal/config"
	"github.com/marcelritzschke/wirelark/internal/paths"
	"github.com/marcelritzschke/wirelark/internal/secfile"
)

// Client sends and updates cards in the configured user's DM as the bot. It
// keeps the config pointer (not a copy of OpenID) so callers can fill in
// the open_id after resolving it (see the init flow).
type Client struct {
	cfg *config.Config
	lc  *lark.Client
}

// New builds a Feishu client with a disk-backed token cache so short-lived
// per-hook processes skip the tenant-token round trip.
func New(cfg *config.Config) (*Client, error) {
	cache, err := newDiskTokenCache()
	if err != nil {
		return nil, err
	}
	return &Client{
		cfg: cfg,
		lc: lark.NewClient(cfg.AppID, cfg.AppSecret,
			// An app exists in one Feishu deployment only, so the host is
			// part of the credentials rather than a preference.
			lark.WithOpenBaseUrl(cfg.OpenBaseURL()),
			lark.WithTokenCache(cache),
			lark.WithLogger(discardLogger{})),
	}, nil
}

// discardLogger silences the SDK's default stderr logging ("[Info] client
// ready", ...): the hook process must stay invisible to the Claude Code
// session that spawned it.
type discardLogger struct{}

func (discardLogger) Debug(context.Context, ...interface{}) {}
func (discardLogger) Info(context.Context, ...interface{})  {}
func (discardLogger) Warn(context.Context, ...interface{})  {}
func (discardLogger) Error(context.Context, ...interface{}) {}

// SendCard delivers an interactive card to the user's DM and returns the
// message id, which later updates to the same card refer to.
func (c *Client) SendCard(ctx context.Context, cardJSON string) (string, error) {
	resp, err := c.lc.Im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType(c.cfg.OpenIDKind)).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			MsgType(larkim.MsgTypeInteractive).
			ReceiveId(c.cfg.OpenID).
			Content(cardJSON).
			Build()).
		Build())
	if err != nil {
		return "", fmt.Errorf("feishu request: %w", err)
	}
	if !resp.Success() {
		return "", apiError(resp.CodeError, resp.RequestId())
	}
	if resp.Data == nil || resp.Data.MessageId == nil {
		return "", errors.New("feishu response carried no message_id")
	}
	return *resp.Data.MessageId, nil
}

// UpdateCard replaces the content of a previously sent card, so one turn's
// progress message can become its completion message.
func (c *Client) UpdateCard(ctx context.Context, messageID, cardJSON string) error {
	resp, err := c.lc.Im.Message.Patch(ctx, larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(cardJSON).
			Build()).
		Build())
	if err != nil {
		return fmt.Errorf("feishu request: %w", err)
	}
	if !resp.Success() {
		return apiError(resp.CodeError, resp.RequestId())
	}
	return nil
}

// ResolveOpenID looks up a user's open_id by email (contact/v3 batch_get_id).
// Used once during init; the resolved id is stored in the config.
func (c *Client) ResolveOpenID(ctx context.Context, email string) (string, error) {
	resp, err := c.lc.Contact.User.BatchGetId(ctx, larkcontact.NewBatchGetIdUserReqBuilder().
		UserIdType(larkcontact.UserIdTypeBatchGetIdUserOpenId).
		Body(larkcontact.NewBatchGetIdUserReqBodyBuilder().
			Emails([]string{email}).
			Build()).
		Build())
	if err != nil {
		return "", fmt.Errorf("feishu request: %w", err)
	}
	if !resp.Success() {
		return "", apiError(resp.CodeError, resp.RequestId())
	}
	if resp.Data == nil {
		return "", errors.New("feishu returned no data for the email")
	}
	for _, u := range resp.Data.UserList {
		if u != nil && u.UserId != nil && *u.UserId != "" {
			return *u.UserId, nil
		}
	}
	return "", fmt.Errorf("no user found for email %s (is the email correct and in the app's availability scope?)", email)
}

// apiError renders a Feishu response that came back with a business error.
// The SDK generates a separate response type per endpoint and they share
// no interface, so the caller hands over the parts that identify the
// failure.
func apiError(e larkcore.CodeError, requestID string) error {
	return fmt.Errorf("feishu error: code=%d msg=%s request_id=%s", e.Code, e.Msg, requestID)
}

// receiveIDType maps the configured id kind to the SDK string the create
// endpoint expects in the receive_id_type query parameter.
func receiveIDType(kind config.OpenIDType) string {
	switch kind {
	case config.OpenIDTypeUserID:
		return larkim.CreateMessageV1ReceiveIDTypeUserId
	case config.OpenIDTypeUnionID:
		return larkim.CreateMessageV1ReceiveIDTypeUnionId
	default:
		return larkim.CreateMessageV1ReceiveIDTypeOpenId
	}
}

// diskTokenCache implements larkcore.Cache over a single JSON file so the
// tenant_access_token survives across per-hook process invocations.
type diskTokenCache struct {
	path string
}

type cacheEntry struct {
	Value    string    `json:"value"`
	ExpireAt time.Time `json:"expire_at"`
}

// newDiskTokenCache returns a cache at token.json in Wirelark's private
// directory.
func newDiskTokenCache() (larkcore.Cache, error) {
	path, err := paths.File("token.json")
	if err != nil {
		return nil, err
	}
	return &diskTokenCache{path: path}, nil
}

func (d *diskTokenCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if !strings.Contains(key, "tenant_access_token") {
		// Only share the tenant token on disk; app tickets etc. stay in memory.
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o700); err != nil {
		return err
	}
	m := map[string]cacheEntry{}
	if data, err := os.ReadFile(d.path); err == nil {
		_ = json.Unmarshal(data, &m) // corrupt file: start fresh
	}
	m[key] = cacheEntry{Value: value, ExpireAt: time.Now().Add(ttl)}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return secfile.WriteAtomic(d.path, data, 0o600)
}

func (d *diskTokenCache) Get(ctx context.Context, key string) (string, error) {
	if !strings.Contains(key, "tenant_access_token") {
		return "", nil
	}
	data, err := os.ReadFile(d.path)
	if err != nil {
		return "", nil // miss
	}
	m := map[string]cacheEntry{}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", nil // corrupt file: miss
	}
	e, ok := m[key]
	if !ok || !time.Now().Before(e.ExpireAt) {
		return "", nil // miss
	}
	return e.Value, nil
}

// SendText delivers a plain text message. Cards are for what the user reads
// and acts on; a short confirmation that their message reached a session is
// neither, and a card for it would turn every message into two.
func (c *Client) SendText(ctx context.Context, text string) (string, error) {
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return "", err
	}
	resp, err := c.lc.Im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType(c.cfg.OpenIDKind)).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			MsgType(larkim.MsgTypeText).
			ReceiveId(c.cfg.OpenID).
			Content(string(content)).
			Build()).
		Build())
	if err != nil {
		return "", fmt.Errorf("feishu request: %w", err)
	}
	if !resp.Success() {
		return "", apiError(resp.CodeError, resp.RequestId())
	}
	if resp.Data == nil || resp.Data.MessageId == nil {
		return "", errors.New("feishu response carried no message_id")
	}
	return *resp.Data.MessageId, nil
}
