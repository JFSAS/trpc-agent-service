// Package telegramregistration confines token-bearing SDK calls and errors.
package telegramregistration

import (
	"context"
	"github.com/go-telegram/bot"
	app "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/application/telegramruntime"
	c "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/connection/domain/accountcatalog"
	"net/http"
	"strconv"
	"time"
)

type Factory struct{}
type client struct {
	bot       *bot.Bot
	transport *http.Transport
}

func (Factory) New(token string) (app.Remote, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxConnsPerHost = 2
	tr.DisableCompression = true
	tr.ResponseHeaderTimeout = 5 * time.Second
	h := &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	b, e := bot.New(token, bot.WithSkipGetMe(), bot.WithHTTPClient(5*time.Second, h))
	if e != nil {
		tr.CloseIdleConnections()
		return nil, c.ErrInvalid
	}
	return &client{b, tr}, nil
}
func (c1 *client) Identity(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u, e := c1.bot.GetMe(ctx)
	if e != nil || u == nil || !u.IsBot || u.ID <= 0 {
		return "", c.ErrUnavailable
	}
	return strconv.FormatInt(u.ID, 10), nil
}
func (c1 *client) Register(ctx context.Context, url, secret string) (bool, error) {
	if !c.ValidWebhookSecret(secret) {
		return false, c.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	yes, e := c1.bot.SetWebhook(ctx, &bot.SetWebhookParams{URL: url, SecretToken: secret, AllowedUpdates: []string{"message", "callback_query"}, MaxConnections: 40, DropPendingUpdates: false})
	if e != nil {
		return false, c.ErrUnavailable
	}
	return yes, nil
}
func (c1 *client) Close() { c1.transport.CloseIdleConnections() }
