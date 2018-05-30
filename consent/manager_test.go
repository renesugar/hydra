/*
 * Copyright © 2015-2018 Aeneas Rekkas <aeneas+oss@aeneas.io>
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * @author		Aeneas Rekkas <aeneas+oss@aeneas.io>
 * @Copyright 	2017-2018 Aeneas Rekkas <aeneas+oss@aeneas.io>
 * @license 	Apache-2.0
 */

package consent

import (
	"flag"
	"log"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/ory/fosite"
	"github.com/ory/hydra/client"
	"github.com/ory/sqlcon/dockertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockConsentRequest(key string, remember bool, rememberFor int, hasError bool, skip bool, authAt bool) (c *ConsentRequest, h *HandledConsentRequest) {
	c = &ConsentRequest{
		OpenIDConnectContext: &OpenIDConnectContext{
			ACRValues: []string{"1" + key, "2" + key},
			UILocales: []string{"fr" + key, "de" + key},
			Display:   "popup" + key,
		},
		RequestedAt:    time.Now().UTC().Add(-time.Hour),
		Client:         &client.Client{ID: "client" + key},
		Subject:        "subject" + key,
		RequestURL:     "https://request-url/path" + key,
		Skip:           skip,
		Challenge:      "challenge" + key,
		RequestedScope: []string{"scopea" + key, "scopeb" + key},
		Verifier:       "verifier" + key,
		CSRF:           "csrf" + key,
	}

	var err *RequestDeniedError
	if hasError {
		err = &RequestDeniedError{
			Name:        "error_name" + key,
			Description: "error_description" + key,
			Hint:        "error_hint,omitempty" + key,
			Code:        100,
			Debug:       "error_debug,omitempty" + key,
		}
	}

	var authenticatedAt time.Time
	if authAt {
		time.Now().UTC().Add(-time.Minute)
	}

	h = &HandledConsentRequest{
		ConsentRequest:  c,
		RememberFor:     rememberFor,
		Remember:        remember,
		Challenge:       "challenge" + key,
		RequestedAt:     time.Now().UTC().Add(-time.Minute),
		AuthenticatedAt: authenticatedAt,
		Error:           err,
	}

	return c, h
}

func mockAuthRequest(key string, authAt bool) (c *AuthenticationRequest, h *HandledAuthenticationRequest) {
	c = &AuthenticationRequest{
		OpenIDConnectContext: &OpenIDConnectContext{
			ACRValues: []string{"1" + key, "2" + key},
			UILocales: []string{"fr" + key, "de" + key},
			Display:   "popup" + key,
		},
		RequestedAt:    time.Now().UTC().Add(-time.Hour),
		Client:         &client.Client{ID: "client" + key},
		Subject:        "subject" + key,
		RequestURL:     "https://request-url/path" + key,
		Skip:           true,
		Challenge:      "challenge" + key,
		RequestedScope: []string{"scopea" + key, "scopeb" + key},
		Verifier:       "verifier" + key,
		CSRF:           "csrf" + key,
	}

	var err = &RequestDeniedError{
		Name:        "error_name" + key,
		Description: "error_description" + key,
		Hint:        "error_hint,omitempty" + key,
		Code:        100,
		Debug:       "error_debug,omitempty" + key,
	}

	var authenticatedAt time.Time
	if authAt {
		time.Now().UTC().Add(-time.Minute)
	}

	h = &HandledAuthenticationRequest{
		AuthenticationRequest: c,
		RememberFor:           120,
		Remember:              true,
		Challenge:             "challenge" + key,
		RequestedAt:           time.Now().UTC().Add(-time.Minute),
		AuthenticatedAt:       authenticatedAt,
		Error:                 err,
		Subject:               c.Subject,
		ACR:                   "acr",
		WasUsed:               false,
	}

	return c, h
}

func connectToPostgres(managers map[string]Manager, c client.Manager) {
	db, err := dockertest.ConnectToTestPostgreSQL()
	if err != nil {
		log.Fatalf("Could not connect to database: %v", err)
		return
	}

	s := NewSQLManager(db, c)
	if _, err := s.CreateSchemas(); err != nil {
		log.Fatalf("Could not connect to database: %v", err)
		return
	}

	managers["postgres"] = s
}

func connectToMySQL(managers map[string]Manager, c client.Manager) {
	db, err := dockertest.ConnectToTestMySQL()
	if err != nil {
		log.Fatalf("Could not connect to database: %v", err)
		return
	}

	s := NewSQLManager(db, c)
	if _, err := s.CreateSchemas(); err != nil {
		log.Fatalf("Could not create mysql schema: %v", err)
		return
	}

	managers["mysql"] = s
}

var clientManager = client.NewMemoryManager(&fosite.BCrypt{WorkFactor: 8})
var managers = map[string]Manager{
	"memory": NewMemoryManager(),
}

func TestMain(m *testing.M) {
	runner := dockertest.Register()

	flag.Parse()
	if !testing.Short() {
		dockertest.Parallel([]func(){
			func() {
				connectToPostgres(managers, clientManager)
			}, func() {
				connectToMySQL(managers, clientManager)
			},
		})
	}

	runner.Exit(m.Run())
}

func TestManagers(t *testing.T) {
	t.Run("case=auth-session", func(t *testing.T) {
		for k, m := range managers {
			t.Run("manager="+k, func(t *testing.T) {
				for _, tc := range []struct {
					s AuthenticationSession
				}{
					{
						s: AuthenticationSession{
							ID:              "session1",
							AuthenticatedAt: time.Now().Round(time.Second).UTC(),
							Subject:         "subject1",
						},
					},
					{
						s: AuthenticationSession{
							ID:              "session2",
							AuthenticatedAt: time.Now().Round(time.Minute).UTC(),
							Subject:         "subject2",
						},
					},
				} {
					t.Run("case=create-get-"+tc.s.ID, func(t *testing.T) {
						_, err := m.GetAuthenticationSession(tc.s.ID)
						require.Error(t, err)

						err = m.CreateAuthenticationSession(&tc.s)
						require.NoError(t, err)

						got, err := m.GetAuthenticationSession(tc.s.ID)
						require.NoError(t, err)
						assert.EqualValues(t, tc.s.ID, got.ID)
						assert.EqualValues(t, tc.s.AuthenticatedAt.Unix(), got.AuthenticatedAt.Unix())
						assert.EqualValues(t, tc.s.Subject, got.Subject)
					})
				}
				for _, tc := range []struct {
					id string
				}{
					{
						id: "session1",
					},
					{
						id: "session2",
					},
				} {
					t.Run("case=delete-get-"+tc.id, func(t *testing.T) {
						err := m.DeleteAuthenticationSession(tc.id)
						require.NoError(t, err)

						_, err = m.GetAuthenticationSession(tc.id)
						require.Error(t, err)
					})
				}
			})
		}
	})

	t.Run("case=consent-request", func(t *testing.T) {
		for k, m := range managers {
			t.Run("manager="+k, func(t *testing.T) {
				for _, tc := range []struct {
					key         string
					remember    bool
					rememberFor int
					hasError    bool
					skip        bool
					authAt      bool
				}{
					{"1", true, 0, false, false, true},
					{"2", true, 0, true, false, true},
					{"3", true, 1, false, false, true},
					{"4", false, 0, false, false, true},
					{"5", true, 120, false, false, true},
					{"6", true, 120, false, true, true},
					{"7", false, 0, false, false, false},
				} {
					t.Run("key="+tc.key, func(t *testing.T) {
						c, h := mockConsentRequest(tc.key, tc.remember, tc.rememberFor, tc.hasError, tc.skip, tc.authAt)
						clientManager.CreateClient(c.Client) // Ignore errors that are caused by duplication

						_, err := m.GetConsentRequest("challenge" + tc.key)
						require.Error(t, err)

						require.NoError(t, m.CreateConsentRequest(c))

						got1, err := m.GetConsentRequest("challenge" + tc.key)
						require.NoError(t, err)
						compareConsentRequest(t, c, got1)

						got1, err = m.HandleConsentRequest("challenge"+tc.key, h)
						require.NoError(t, err)
						compareConsentRequest(t, c, got1)

						got2, err := m.VerifyAndInvalidateConsentRequest("verifier" + tc.key)
						require.NoError(t, err)
						compareConsentRequest(t, c, got2.ConsentRequest)
						assert.Equal(t, c.Challenge, got2.Challenge)

						_, err = m.VerifyAndInvalidateConsentRequest("verifier" + tc.key)
						require.Error(t, err)
					})
				}

				for _, tc := range []struct {
					keyC           string
					keyS           string
					expectedLength int
				}{
					{"1", "1", 1},
					{"2", "2", 0},
					{"3", "3", 0},
					{"4", "4", 0},
					{"1", "2", 0},
					{"2", "1", 0},
					{"5", "5", 1},
					{"6", "6", 0},
				} {
					t.Run("key="+tc.keyC+"-"+tc.keyS, func(t *testing.T) {
						rs, err := m.FindPreviouslyGrantedConsentRequests("client"+tc.keyC, "subject"+tc.keyS)
						require.NoError(t, err)
						assert.Len(t, rs, tc.expectedLength)
					})
				}
			})
		}
	})

	t.Run("case=auth-request", func(t *testing.T) {
		for k, m := range managers {
			t.Run("manager="+k, func(t *testing.T) {
				for _, tc := range []struct {
					key    string
					authAt bool
				}{
					{"1", true},
					{"2", true},
					{"3", true},
					{"4", true},
					{"5", true},
					{"6", false},
				} {
					t.Run("key="+tc.key, func(t *testing.T) {
						c, h := mockAuthRequest(tc.key, tc.authAt)
						clientManager.CreateClient(c.Client) // Ignore errors that are caused by duplication

						_, err := m.GetAuthenticationRequest("challenge" + tc.key)
						require.Error(t, err)

						require.NoError(t, m.CreateAuthenticationRequest(c))

						got1, err := m.GetAuthenticationRequest("challenge" + tc.key)
						require.NoError(t, err)
						compareAuthenticationRequest(t, c, got1)

						got1, err = m.HandleAuthenticationRequest("challenge"+tc.key, h)
						require.NoError(t, err)
						compareAuthenticationRequest(t, c, got1)

						got2, err := m.VerifyAndInvalidateAuthenticationRequest("verifier" + tc.key)
						require.NoError(t, err)
						compareAuthenticationRequest(t, c, got2.AuthenticationRequest)
						assert.Equal(t, c.Challenge, got2.Challenge)

						_, err = m.VerifyAndInvalidateAuthenticationRequest("verifier" + tc.key)
						require.Error(t, err)
					})
				}
			})
		}
	})
}

func compareAuthenticationRequest(t *testing.T, a, b *AuthenticationRequest) {
	assert.EqualValues(t, a.Client.ID, b.Client.ID)
	assert.EqualValues(t, a.Challenge, b.Challenge)
	assert.EqualValues(t, *a.OpenIDConnectContext, *b.OpenIDConnectContext)
	assert.EqualValues(t, a.Subject, b.Subject)
	assert.EqualValues(t, a.RequestedScope, b.RequestedScope)
	assert.EqualValues(t, a.Verifier, b.Verifier)
	assert.EqualValues(t, a.RequestURL, b.RequestURL)
	assert.EqualValues(t, a.CSRF, b.CSRF)
	assert.EqualValues(t, a.Skip, b.Skip)
}

func compareConsentRequest(t *testing.T, a, b *ConsentRequest) {
	assert.EqualValues(t, a.Client.ID, b.Client.ID)
	assert.EqualValues(t, a.Challenge, b.Challenge)
	assert.EqualValues(t, *a.OpenIDConnectContext, *b.OpenIDConnectContext)
	assert.EqualValues(t, a.Subject, b.Subject)
	assert.EqualValues(t, a.RequestedScope, b.RequestedScope)
	assert.EqualValues(t, a.Verifier, b.Verifier)
	assert.EqualValues(t, a.RequestURL, b.RequestURL)
	assert.EqualValues(t, a.CSRF, b.CSRF)
	assert.EqualValues(t, a.Skip, b.Skip)
}
