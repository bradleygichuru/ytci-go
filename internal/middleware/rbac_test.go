package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

func wrapRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, middleware.CtxRole, role)
}

func wrapUserID(ctx context.Context, uid string) context.Context {
	return context.WithValue(ctx, middleware.CtxUserID, uid)
}

func TestAdminGateAcceptsAdmin(t *testing.T) {
	var handled bool
	mw := middleware.AdminGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
		w.WriteHeader(http.StatusOK)
	}))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(wrapRole(r.Context(), "super_admin"))
		mw.ServeHTTP(w, r)
	}))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, handled)
}

func TestAdminGateRejectsUser(t *testing.T) {
	var handled bool
	mw := middleware.AdminGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
		w.WriteHeader(http.StatusOK)
	}))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(wrapRole(r.Context(), "user"))
		mw.ServeHTTP(w, r)
	}))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.False(t, handled)
}

func TestAdminGateRejectsEmptyRole(t *testing.T) {
	var handled bool
	mw := middleware.AdminGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
		w.WriteHeader(http.StatusOK)
	}))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(wrapRole(r.Context(), ""))
		mw.ServeHTTP(w, r)
	}))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.False(t, handled)
}

func TestAdminGateRejectsCountyOfficer(t *testing.T) {
	var handled bool
	mw := middleware.AdminGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
		w.WriteHeader(http.StatusOK)
	}))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(wrapRole(r.Context(), "county_officer"))
		mw.ServeHTTP(w, r)
	}))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.False(t, handled)
}

func TestAuthGateAcceptsAuthUser(t *testing.T) {
	var handled bool
	mw := middleware.AuthGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
		w.WriteHeader(http.StatusOK)
	}))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(wrapUserID(r.Context(), "user-123"))
		mw.ServeHTTP(w, r)
	}))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, handled)
}

func TestAuthGateRejectsMissingUser(t *testing.T) {
	var handled bool
	mw := middleware.AuthGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
		w.WriteHeader(http.StatusOK)
	}))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(wrapUserID(r.Context(), ""))
		mw.ServeHTTP(w, r)
	}))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.False(t, handled)
}
