package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"growth-lms/internal/config"
)

// Session is the subset of a Supabase Auth token response the backend
// needs to hand back to the caller (as a Go-managed session cookie) or to
// use for a subsequent authenticated call to Supabase.
type Session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	User         struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

// Client is everything the auth handlers need from Supabase's GoTrue Auth
// API. It is an interface so handler tests can inject a fake instead of
// depending on a live Supabase project.
type Client interface {
	SignUp(ctx context.Context, email, password string) error
	VerifyOTP(ctx context.Context, tokenHash, otpType string) (*Session, error)
	SignInWithPassword(ctx context.Context, email, password string) (*Session, error)
	RequestPasswordReset(ctx context.Context, email string) error
	UpdatePassword(ctx context.Context, accessToken, newPassword string) error
	SignOut(ctx context.Context, accessToken string) error
	AdminDeleteUser(ctx context.Context, userID string) error
}

// SupabaseClient is the real Client implementation, talking to Supabase's
// GoTrue REST API directly (no official Go SDK is mature enough for the
// Admin API surface this needs).
type SupabaseClient struct {
	baseURL        string
	anonKey        string
	serviceRoleKey string
	http           *http.Client
}

// NewSupabaseClient builds a SupabaseClient from the application's
// Supabase config.
func NewSupabaseClient(cfg config.SupabaseConfig) *SupabaseClient {
	return &SupabaseClient{
		baseURL:        cfg.URL,
		anonKey:        cfg.AnonKey,
		serviceRoleKey: cfg.ServiceRoleKey,
		http:           &http.Client{Timeout: 10 * time.Second},
	}
}

// authAPIError mirrors GoTrue's {"error_code": "...", "msg": "..."} (or
// legacy {"error": "..."}) error body, used only to detect success/failure
// without leaking upstream error text verbatim to end users.
type authAPIError struct {
	Msg   string `json:"msg"`
	Error string `json:"error"`
}

func (c *SupabaseClient) do(ctx context.Context, method, path string, apiKey string, bearer string, body any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("auth: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("auth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", apiKey)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: request to supabase: %w", err)
	}
	return resp, nil
}

func checkStatus(resp *http.Response) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	var apiErr authAPIError
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &apiErr)
	msg := apiErr.Msg
	if msg == "" {
		msg = apiErr.Error
	}
	if msg == "" {
		msg = fmt.Sprintf("unexpected status %d", resp.StatusCode)
	}
	return fmt.Errorf("auth: %s", msg)
}

func (c *SupabaseClient) SignUp(ctx context.Context, email, password string) error {
	resp, err := c.do(ctx, http.MethodPost, "/auth/v1/signup", c.anonKey, "", map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return err
	}
	return checkStatus(resp)
}

// VerifyOTP exchanges an email-verification or password-recovery token
// hash (from the link Supabase emailed the user) for a session, per
// GoTrue's POST /auth/v1/verify.
func (c *SupabaseClient) VerifyOTP(ctx context.Context, tokenHash, otpType string) (*Session, error) {
	resp, err := c.do(ctx, http.MethodPost, "/auth/v1/verify", c.anonKey, "", map[string]string{
		"token_hash": tokenHash,
		"type":       otpType,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, checkStatus(resp)
	}
	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("auth: decode session: %w", err)
	}
	return &session, nil
}

func (c *SupabaseClient) SignInWithPassword(ctx context.Context, email, password string) (*Session, error) {
	resp, err := c.do(ctx, http.MethodPost, "/auth/v1/token?grant_type=password", c.anonKey, "", map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, checkStatus(resp)
	}
	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("auth: decode session: %w", err)
	}
	return &session, nil
}

func (c *SupabaseClient) RequestPasswordReset(ctx context.Context, email string) error {
	resp, err := c.do(ctx, http.MethodPost, "/auth/v1/recover", c.anonKey, "", map[string]string{
		"email": email,
	})
	if err != nil {
		return err
	}
	return checkStatus(resp)
}

func (c *SupabaseClient) UpdatePassword(ctx context.Context, accessToken, newPassword string) error {
	resp, err := c.do(ctx, http.MethodPut, "/auth/v1/user", c.anonKey, accessToken, map[string]string{
		"password": newPassword,
	})
	if err != nil {
		return err
	}
	return checkStatus(resp)
}

func (c *SupabaseClient) SignOut(ctx context.Context, accessToken string) error {
	resp, err := c.do(ctx, http.MethodPost, "/auth/v1/logout", c.anonKey, accessToken, nil)
	if err != nil {
		return err
	}
	return checkStatus(resp)
}

func (c *SupabaseClient) AdminDeleteUser(ctx context.Context, userID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/auth/v1/admin/users/"+userID, c.serviceRoleKey, c.serviceRoleKey, nil)
	if err != nil {
		return err
	}
	return checkStatus(resp)
}
