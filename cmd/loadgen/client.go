package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	reCSRFToken = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)
	reRoomID    = regexp.MustCompile(`hx-get="/rooms/(\d+)"`)
	reInviteID  = regexp.MustCompile(`hx-post="/invites/(\d+)/accept"`)
)

type userClient struct {
	http      *http.Client
	baseURL   string
	username  string
	password  string
	userID    int64
	csrfToken string
}

func newUserClient(baseURL, username, password string) (*userClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &userClient{
		http:     &http.Client{Jar: jar},
		baseURL:  baseURL,
		username: username,
		password: password,
	}, nil
}

func (c *userClient) seedCSRF(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	m := reCSRFToken.FindSubmatch(body)
	if m == nil {
		return fmt.Errorf("csrf-token meta tag not found in response")
	}
	c.csrfToken = string(m[1])
	return nil
}

func (c *userClient) register(ctx context.Context) error {
	body, err := c.post(ctx, "/users", url.Values{
		"username":         {c.username},
		"password":         {c.password},
		"password_confirm": {c.password},
	})
	if err != nil {
		return err
	}
	// session cookie is set by the server after registration; extract user ID from it
	id, err := c.userIDFromSession()
	if err != nil {
		return fmt.Errorf("parse user id after register: %w (body snippet: %.200s)", err, body)
	}
	c.userID = id
	return nil
}

// userIDFromSession parses the session cookie placed in the jar by the server.
// Cookie value format: "userID|timestamp|version.<hmac-sig>"
func (c *userClient) userIDFromSession() (int64, error) {
	u, _ := url.Parse(c.baseURL)
	for _, cookie := range c.http.Jar.Cookies(u) {
		if cookie.Name != "session" {
			continue
		}
		payload, _, ok := strings.Cut(cookie.Value, ".")
		if !ok {
			return 0, fmt.Errorf("malformed session cookie")
		}
		parts := strings.SplitN(payload, "|", 3)
		if len(parts) != 3 {
			return 0, fmt.Errorf("unexpected session payload format")
		}
		return strconv.ParseInt(parts[0], 10, 64)
	}
	return 0, fmt.Errorf("session cookie not found in jar")
}

func (c *userClient) createRoom(ctx context.Context, displayName string) (int64, error) {
	body, err := c.post(ctx, "/rooms", url.Values{"display_name": {displayName}})
	if err != nil {
		return 0, err
	}
	m := reRoomID.FindSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("room ID not found in response (body snippet: %.200s)", body)
	}
	return strconv.ParseInt(string(m[1]), 10, 64)
}

func (c *userClient) inviteUser(ctx context.Context, roomID int64, username string) error {
	_, err := c.post(ctx, fmt.Sprintf("/rooms/%d/invites", roomID), url.Values{
		"invitee_username": {username},
	})
	return err
}

func (c *userClient) getInviteID(ctx context.Context) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/content/invites", nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	m := reInviteID.FindSubmatch(body)
	if m == nil {
		return 0, fmt.Errorf("no pending invite found (body snippet: %.200s)", body)
	}
	return strconv.ParseInt(string(m[1]), 10, 64)
}

func (c *userClient) acceptInvite(ctx context.Context, inviteID int64) error {
	_, err := c.post(ctx, fmt.Sprintf("/invites/%d/accept", inviteID), nil)
	return err
}

func (c *userClient) deleteUser(ctx context.Context) error {
	vals := url.Values{"current_password": {c.password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("%s/users/%d", c.baseURL, c.userID),
		strings.NewReader(vals.Encode()),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Csrf-Token", c.csrfToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete user: status %d", resp.StatusCode)
	}
	return nil
}

// post sends a POST with url-encoded form values and the current CSRF token.
// It returns the response body.
func (c *userClient) post(ctx context.Context, path string, vals url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+path,
		strings.NewReader(vals.Encode()),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Csrf-Token", c.csrfToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return body, fmt.Errorf("POST %s: status %d", path, resp.StatusCode)
	}
	return body, nil
}
