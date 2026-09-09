package workerhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	governancev1 "github.com/liuzengh/trpc-agent-service/api/runtime/governance/v1"
	managementv1 "github.com/liuzengh/trpc-agent-service/api/runtime/management/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/runmanagement/application"
)

type Client struct {
	client *http.Client
	base   *url.URL
}

func New(client *http.Client, base string) (*Client, error) {
	u, err := url.Parse(base)
	if client == nil || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, application.ErrUnavailable
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return &Client{client: client, base: u}, nil
}

func (c *Client) get(ctx context.Context, path string, target any) error {
	u := *c.base
	u.Path += path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return application.ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	response, err := c.client.Do(req)
	if err != nil {
		return application.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return application.ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return application.ErrUnavailable
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return application.ErrUnavailable
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		return application.ErrUnavailable
	}
	return nil
}

func pagePath(tenant, resource string, offset, limit int) string {
	return fmt.Sprintf("/internal/v1/management/tenants/%s/%s?offset=%s&limit=%s", url.PathEscape(tenant), resource, strconv.Itoa(offset), strconv.Itoa(limit))
}

func (c *Client) ListRuns(ctx context.Context, tenant string, offset, limit int) (managementv1.RunPage, error) {
	var page managementv1.RunPage
	err := c.get(ctx, pagePath(tenant, "runs", offset, limit), &page)
	return page, err
}
func (c *Client) GetRun(ctx context.Context, tenant, runID string) (managementv1.RunDetail, error) {
	var run managementv1.RunDetail
	err := c.get(ctx, "/internal/v1/management/tenants/"+url.PathEscape(tenant)+"/runs/"+url.PathEscape(runID), &run)
	return run, err
}
func (c *Client) ListAudit(ctx context.Context, tenant string, offset, limit int) (managementv1.AuditPage, error) {
	var page managementv1.AuditPage
	err := c.get(ctx, pagePath(tenant, "audit-events", offset, limit), &page)
	return page, err
}

func (c *Client) Usage(ctx context.Context, tenant string) (governancev1.UsageSummary, error) {
	var summary governancev1.UsageSummary
	err := c.get(ctx, "/internal/v1/management/tenants/"+url.PathEscape(tenant)+"/usage-summary", &summary)
	return summary, err
}
