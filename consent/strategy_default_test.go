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
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/sessions"
	"github.com/julienschmidt/httprouter"
	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/openid"
	"github.com/ory/fosite/token/jwt"
	"github.com/ory/herodot"
	"github.com/ory/hydra/client"
	"github.com/ory/hydra/pkg"
	"github.com/ory/hydra/sdk/go/hydra/swagger"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustRSAKey() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		panic(err)
	}
	return key
}

func mustParseURL(t *testing.T, u string) *url.URL {
	uu, err := url.Parse(u)
	require.NoError(t, err)
	return uu
}

func mockProvider(h *func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*h)(w, r)
	}))
}

func noopHandler(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func newCookieJar() *cookiejar.Jar {
	c, _ := cookiejar.New(&cookiejar.Options{})
	return c
}

func TestStrategy(t *testing.T) {
	var lph, cph, aph func(w http.ResponseWriter, r *http.Request)
	lp := mockProvider(&lph)
	cp := mockProvider(&cph)
	ap := mockProvider(&aph)

	jwts := &jwt.RS256JWTStrategy{
		PrivateKey: pkg.MustINSECURELOWENTROPYRSAKEYFORTEST(),
	}

	fooUserIDToken, _, err := jwts.Generate((jwt.IDTokenClaims{
		Subject:   "foouser",
		ExpiresAt: time.Now().Add(time.Hour),
		IssuedAt:  time.Now(),
	}).ToMapClaims(), jwt.NewHeaders())
	require.NoError(t, err)

	writer := herodot.NewJSONWriter(nil)
	manager := NewMemoryManager()
	handler := NewHandler(writer, manager)
	router := httprouter.New()
	handler.SetRoutes(router)
	api := httptest.NewServer(router)
	strategy := NewStrategy(
		lp.URL,
		cp.URL,
		ap.URL,
		"/oauth2/auth",
		manager,
		sessions.NewCookieStore([]byte("dummy-secret-yay")),
		fosite.ExactScopeStrategy,
		false,
		time.Hour,
		jwts,
		openid.NewOpenIDConnectRequestValidator(nil, jwts),
	)
	apiClient := swagger.NewOAuth2ApiWithBasePath(api.URL)

	persistentCJ := newCookieJar()
	persistentCJ2 := newCookieJar()

	for k, tc := range []struct {
		setup                 func()
		d                     string
		lv                    string
		cv                    string
		lph, cph              func(t *testing.T) func(w http.ResponseWriter, r *http.Request)
		req                   fosite.AuthorizeRequest
		expectSession         *HandledConsentRequest
		expectErr             []bool
		expectErrType         []error
		expectFinalStatusCode int
		prompt                string
		maxAge                string
		idTokenHint           string
		jar                   http.CookieJar
	}{
		{
			d:                     "This should fail because a login verifier was given that doesn't exist in the store",
			req:                   fosite.AuthorizeRequest{Request: fosite.Request{Client: &client.Client{ID: "client-id"}}},
			lv:                    "invalid",
			expectErrType:         []error{fosite.ErrAccessDenied},
			expectErr:             []bool{true},
			expectFinalStatusCode: http.StatusForbidden,
		}, {
			d:                     "This should fail because a consent verifier was given but no login verifier",
			req:                   fosite.AuthorizeRequest{Request: fosite.Request{Client: &client.Client{ID: "client-id"}}},
			lv:                    "",
			cv:                    "invalid",
			expectErrType:         []error{fosite.ErrAccessDenied},
			expectErr:             []bool{true},
			expectFinalStatusCode: http.StatusForbidden,
		},
		{
			d:   "This should fail because the request was redirected but the login endpoint doesn't do anything (like redirecting back)",
			req: fosite.AuthorizeRequest{Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					lr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.NotEmpty(t, lr.Challenge)
					assert.EqualValues(t, r.URL.Query().Get("login_challenge"), lr.Challenge)
					assert.EqualValues(t, "client-id", lr.Client.Id)
					assert.EqualValues(t, []string{"scope-a"}, lr.RequestedScope)
					assert.Contains(t, lr.RequestUrl, "/oauth2/auth?login_verifier=&consent_verifier=&")
					assert.EqualValues(t, false, lr.Skip)
					assert.EqualValues(t, "", lr.Subject)
					assert.EqualValues(t, swagger.OpenIdConnectContext{AcrValues: nil, Display: "", UiLocales: nil}, lr.OidcContext)
					w.WriteHeader(http.StatusNoContent)
				}
			},
			expectFinalStatusCode: http.StatusNoContent,
			expectErrType:         []error{ErrAbortOAuth2Request},
			expectErr:             []bool{true},
		},
		{
			d:   "This should fail because the request was redirected but the login endpoint rejected the request",
			req: fosite.AuthorizeRequest{Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					lr, res, err := apiClient.RejectLoginRequest(r.URL.Query().Get("login_challenge"), swagger.RejectRequest{
						Error_:           fosite.ErrInteractionRequired.Name,
						ErrorDebug:       fosite.ErrInteractionRequired.Debug,
						ErrorDescription: fosite.ErrInteractionRequired.Description,
						ErrorHint:        fosite.ErrInteractionRequired.Hint,
						StatusCode:       int64(fosite.ErrInteractionRequired.Code),
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.NotEmpty(t, lr.RedirectTo)
					http.Redirect(w, r, lr.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusBadRequest,
			expectErrType:         []error{ErrAbortOAuth2Request, fosite.ErrInteractionRequired},
			expectErr:             []bool{true, true},
		},
		{
			d:   "This should fail because no cookie jar / invalid csrf",
			req: fosite.AuthorizeRequest{Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			lph: passAuthentication(apiClient, false),
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					// this should never be called because csrf doesn't make it that far
					require.True(t, false)
				}
			},
			expectFinalStatusCode: http.StatusForbidden,
			expectErrType:         []error{ErrAbortOAuth2Request, fosite.ErrRequestForbidden},
			expectErr:             []bool{true, true},
		},
		{
			d:   "This should fail because consent endpoints idles after login was granted - but consent endpoint should be called because cookie jar exists",
			jar: newCookieJar(),
			req: fosite.AuthorizeRequest{Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			lph: passAuthentication(apiClient, false),
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					lr, res, err := apiClient.GetConsentRequest(r.URL.Query().Get("consent_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.NotEmpty(t, lr.Challenge)
					assert.EqualValues(t, r.URL.Query().Get("consent_challenge"), lr.Challenge)
					assert.EqualValues(t, "client-id", lr.Client.Id)
					assert.EqualValues(t, []string{"scope-a"}, lr.RequestedScope)
					assert.Contains(t, lr.RequestUrl, "/oauth2/auth?login_verifier=&consent_verifier=&")
					assert.EqualValues(t, false, lr.Skip)
					assert.EqualValues(t, "user", lr.Subject)
					assert.EqualValues(t, swagger.OpenIdConnectContext{AcrValues: nil, Display: "", UiLocales: nil}, lr.OidcContext)
					w.WriteHeader(http.StatusNoContent)
				}
			},
			expectFinalStatusCode: http.StatusNoContent,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request},
			expectErr:             []bool{true, true},
		},
		{
			d:   "This should fail because consent verifier was set but does not exist",
			jar: newCookieJar(),
			cv:  "invalid",
			req: fosite.AuthorizeRequest{Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			expectFinalStatusCode: http.StatusForbidden,
			expectErrType:         []error{fosite.ErrAccessDenied},
			expectErr:             []bool{true},
		},
		{
			d:   "This should fail because consent endpoints denies the request after login was granted",
			req: fosite.AuthorizeRequest{Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar: newCookieJar(),
			lph: passAuthentication(apiClient, false),
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					v, res, err := apiClient.RejectConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.RejectRequest{
						Error_:           fosite.ErrInteractionRequired.Name,
						ErrorDebug:       fosite.ErrInteractionRequired.Debug,
						ErrorDescription: fosite.ErrInteractionRequired.Description,
						ErrorHint:        fosite.ErrInteractionRequired.Hint,
						StatusCode:       int64(fosite.ErrInteractionRequired.Code),
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusBadRequest,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, fosite.ErrInteractionRequired},
			expectErr:             []bool{true, true, true},
		},
		{
			d:   "This should pass because login and consent have been granted",
			req: fosite.AuthorizeRequest{Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar: newCookieJar(),
			lph: passAuthentication(apiClient, false),
			cph: passAuthorization(apiClient, false),
			expectFinalStatusCode: http.StatusOK,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
			expectErr:             []bool{true, true, false},
			expectSession: &HandledConsentRequest{
				ConsentRequest: &ConsentRequest{Subject: "user"},
				GrantedScope:   []string{"scope-a"},
				Remember:       false,
				RememberFor:    0,
				Session: &ConsentRequestSessionData{
					AccessToken: map[string]interface{}{"foo": "bar"},
					IDToken:     map[string]interface{}{"bar": "baz"},
				},
			},
		},
		{
			d:   "This should pass because login and consent have been granted, this time we remember the decision",
			req: fosite.AuthorizeRequest{Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar: persistentCJ,
			lph: passAuthentication(apiClient, true),
			cph: passAuthorization(apiClient, true),
			expectFinalStatusCode: http.StatusOK,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
			expectErr:             []bool{true, true, false},
			expectSession: &HandledConsentRequest{
				ConsentRequest: &ConsentRequest{Subject: "user"},
				GrantedScope:   []string{"scope-a"},
				Remember:       true,
				RememberFor:    0,
				Session: &ConsentRequestSessionData{
					AccessToken: map[string]interface{}{"foo": "bar"},
					IDToken:     map[string]interface{}{"bar": "baz"},
				},
			},
		},
		{
			d:      "This should fail because prompt=none, client is public, and redirection scheme is not HTTPS but a custom scheme",
			req:    fosite.AuthorizeRequest{RedirectURI: mustParseURL(t, "custom://redirection-scheme/path"), Request: fosite.Request{Client: &client.Client{Public: true, ID: "client-id"}, Scopes: []string{"scope-a"}}},
			prompt: "none",
			jar:    persistentCJ,
			lph:    passAuthentication(apiClient, false),
			expectFinalStatusCode: fosite.ErrConsentRequired.StatusCode(),
			expectErrType:         []error{ErrAbortOAuth2Request, fosite.ErrConsentRequired},
			expectErr:             []bool{true, true},
		},
		// This test is disabled because it breaks OIDC Conformity Tests
		//{
		//	d:   "This should pass but require consent because it's not an authorization_code flow",
		//	req: fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"token", "code", "id_token"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
		//	jar: persistentCJ,
		//	lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
		//		return func(w http.ResponseWriter, r *http.Request) {
		//			rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
		//			require.NoError(t, err)
		//			require.EqualValues(t, http.StatusOK, res.StatusCode)
		//			assert.True(t, rr.Skip)
		//			assert.Equal(t, "user", rr.Subject)
		//
		//			v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
		//				Subject:     "user",
		//				Remember:    false,
		//				RememberFor: 0,
		//				Acr:         "1",
		//			})
		//			require.NoError(t, err)
		//			require.EqualValues(t, http.StatusOK, res.StatusCode)
		//			require.NotEmpty(t, v.RedirectTo)
		//			http.Redirect(w, r, v.RedirectTo, http.StatusFound)
		//		}
		//	},
		//	cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
		//		return func(w http.ResponseWriter, r *http.Request) {
		//			rr, res, err := apiClient.GetConsentRequest(r.URL.Query().Get("consent_challenge"))
		//			require.NoError(t, err)
		//			require.EqualValues(t, http.StatusOK, res.StatusCode)
		//			assert.False(t, rr.Skip)
		//			assert.Equal(t, "client-id", rr.Client.Id)
		//			assert.Equal(t, "user", rr.Subject)
		//
		//			v, res, err := apiClient.AcceptConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.AcceptConsentRequest{
		//				GrantScope:  []string{"scope-a"},
		//				Remember:    false,
		//				RememberFor: 0,
		//				Session: swagger.ConsentRequestSession{
		//					AccessToken: map[string]interface{}{"foo": "bar"},
		//					IdToken:     map[string]interface{}{"bar": "baz"},
		//				},
		//			})
		//			require.NoError(t, err)
		//			require.EqualValues(t, http.StatusOK, res.StatusCode)
		//			require.NotEmpty(t, v.RedirectTo)
		//			http.Redirect(w, r, v.RedirectTo, http.StatusFound)
		//		}
		//	},
		//	expectFinalStatusCode: http.StatusOK,
		//	expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
		//	expectErr:             []bool{true, true, false},
		//	expectSession: &HandledConsentRequest{
		//		ConsentRequest: &ConsentRequest{Subject: "user"},
		//		GrantedScope:   []string{"scope-a"},
		//		Remember:       false,
		//		RememberFor:    0,
		//		Session: &ConsentRequestSessionData{
		//			AccessToken: map[string]interface{}{"foo": "bar"},
		//			IDToken:     map[string]interface{}{"bar": "baz"},
		//		},
		//	},
		//},
		{
			d:   "This should fail at login screen because subject from accept does not match subject from session",
			req: fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar: persistentCJ,
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.True(t, rr.Skip)
					assert.Equal(t, "user", rr.Subject)

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:     "fooser",
						Remember:    false,
						RememberFor: 0,
						Acr:         "1",
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusBadRequest, res.StatusCode)
					require.Empty(t, v.RedirectTo)
					w.WriteHeader(http.StatusBadRequest)
				}
			},
			expectFinalStatusCode: http.StatusBadRequest,
			expectErrType:         []error{ErrAbortOAuth2Request},
			expectErr:             []bool{true},
		},
		{
			d:   "This should pass and confirm previous authentication and consent because it is a authorization_code",
			req: fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id", Secret: "should-not-be-included"}, Scopes: []string{"scope-a"}}},
			jar: persistentCJ,
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.True(t, rr.Skip)
					assert.Equal(t, "user", rr.Subject)
					assert.Empty(t, rr.Client.ClientSecret)

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:     "user",
						Remember:    false,
						RememberFor: 0,
						Acr:         "1",
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetConsentRequest(r.URL.Query().Get("consent_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.True(t, rr.Skip)
					assert.Equal(t, "client-id", rr.Client.Id)
					assert.Equal(t, "user", rr.Subject)
					assert.Empty(t, rr.Client.ClientSecret)

					v, res, err := apiClient.AcceptConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.AcceptConsentRequest{
						GrantScope:  []string{"scope-a"},
						Remember:    false,
						RememberFor: 0,
						Session: swagger.ConsentRequestSession{
							AccessToken: map[string]interface{}{"foo": "bar"},
							IdToken:     map[string]interface{}{"bar": "baz"},
						},
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusOK,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
			expectErr:             []bool{true, true, false},
			expectSession: &HandledConsentRequest{
				ConsentRequest: &ConsentRequest{Subject: "user"},
				GrantedScope:   []string{"scope-a"},
				Remember:       false,
				RememberFor:    0,
				Session: &ConsentRequestSessionData{
					AccessToken: map[string]interface{}{"foo": "bar"},
					IDToken:     map[string]interface{}{"bar": "baz"},
				},
			},
		},
		{
			d:      "This should pass and require re-authentication although session is set (because prompt=login)",
			req:    fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar:    persistentCJ,
			prompt: "login+consent",
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.False(t, rr.Skip)

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:     "user",
						Remember:    true,
						RememberFor: 0,
						Acr:         "1",
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetConsentRequest(r.URL.Query().Get("consent_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.False(t, rr.Skip)

					v, res, err := apiClient.AcceptConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.AcceptConsentRequest{
						GrantScope:  []string{"scope-a"},
						Remember:    true,
						RememberFor: 0,
						Session: swagger.ConsentRequestSession{
							AccessToken: map[string]interface{}{"foo": "bar"},
							IdToken:     map[string]interface{}{"bar": "baz"},
						},
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusOK,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
			expectErr:             []bool{true, true, false},
			expectSession: &HandledConsentRequest{
				ConsentRequest: &ConsentRequest{Subject: "user"},
				GrantedScope:   []string{"scope-a"},
				Remember:       true,
				RememberFor:    0,
				Session: &ConsentRequestSessionData{
					AccessToken: map[string]interface{}{"foo": "bar"},
					IDToken:     map[string]interface{}{"bar": "baz"},
				},
			},
		},
		{
			d:      "This should pass and require re-authentication although session is set (because max_age=1)",
			req:    fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar:    persistentCJ,
			maxAge: "1",
			setup: func() {
				time.Sleep(time.Second * 2)
			},
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.False(t, rr.Skip)

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:     "user",
						Remember:    true,
						RememberFor: 0,
						Acr:         "1",
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode, res.Payload)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetConsentRequest(r.URL.Query().Get("consent_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.True(t, rr.Skip)

					v, res, err := apiClient.AcceptConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.AcceptConsentRequest{
						GrantScope:  []string{"scope-a"},
						Remember:    false,
						RememberFor: 0,
						Session: swagger.ConsentRequestSession{
							AccessToken: map[string]interface{}{"foo": "bar"},
							IdToken:     map[string]interface{}{"bar": "baz"},
						},
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusOK,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
			expectErr:             []bool{true, true, false},
			expectSession: &HandledConsentRequest{
				ConsentRequest: &ConsentRequest{Subject: "user"},
				GrantedScope:   []string{"scope-a"},
				Remember:       false,
				RememberFor:    0,
				Session: &ConsentRequestSessionData{
					AccessToken: map[string]interface{}{"foo": "bar"},
					IDToken:     map[string]interface{}{"bar": "baz"},
				},
			},
		},
		{
			d:   "This should fail because max_age=1 but prompt=none",
			req: fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar: persistentCJ,
			setup: func() {
				time.Sleep(time.Second * 2)
			},
			maxAge:                "1",
			prompt:                "none",
			expectFinalStatusCode: fosite.ErrLoginRequired.StatusCode(),
			expectErrType:         []error{fosite.ErrLoginRequired},
			expectErr:             []bool{true},
		},
		{
			d:   "This should fail because skip is true and remember as well when doing login",
			req: fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar: persistentCJ,
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.True(t, rr.Skip)

					_, res, err = apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:     "user",
						Remember:    true,
						RememberFor: 0,
						Acr:         "1",
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusBadRequest, res.StatusCode)

					w.WriteHeader(http.StatusNoContent)
				}
			},
			expectFinalStatusCode: http.StatusNoContent,
			expectErrType:         []error{ErrAbortOAuth2Request},
			expectErr:             []bool{true},
		},
		{
			d:   "This fail because skip is true and remember as well when doing consent",
			req: fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar: persistentCJ,
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.True(t, rr.Skip)
					assert.Equal(t, "user", rr.Subject)

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:     "user",
						Remember:    false,
						RememberFor: 0,
						Acr:         "1",
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetConsentRequest(r.URL.Query().Get("consent_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.True(t, rr.Skip)

					_, res, err = apiClient.AcceptConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.AcceptConsentRequest{
						GrantScope:  []string{"scope-a"},
						Remember:    true,
						RememberFor: 0,
						Session: swagger.ConsentRequestSession{
							AccessToken: map[string]interface{}{"foo": "bar"},
							IdToken:     map[string]interface{}{"bar": "baz"},
						},
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusBadRequest, res.StatusCode)

					w.WriteHeader(http.StatusNoContent)
				}
			},
			expectFinalStatusCode: http.StatusNoContent,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request},
			expectErr:             []bool{true, true},
		},
		{
			d:      "This should fail because prompt is none but no auth session exists",
			prompt: "none",
			req:    fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar:    newCookieJar(),
			expectFinalStatusCode: http.StatusBadRequest,
			expectErrType:         []error{fosite.ErrLoginRequired},
			expectErr:             []bool{true},
		},
		{
			d:      "This should fail because prompt is none and consent is missing a permission which requires re-authorization of the app",
			prompt: "none",
			req:    fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a", "this-scope-has-not-been-granted-before"}}},
			jar:    persistentCJ,
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.True(t, rr.Skip)
					assert.Equal(t, "user", rr.Subject)

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:     "user",
						Remember:    false,
						RememberFor: 0,
						Acr:         "1",
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusBadRequest,
			expectErrType:         []error{ErrAbortOAuth2Request, fosite.ErrConsentRequired},
			expectErr:             []bool{true, true},
		},
		{
			d:      "This pass and properly require authentication as well as authorization because prompt is set to login and consent - although previous session exists",
			req:    fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar:    persistentCJ,
			prompt: "login+consent",
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.False(t, rr.Skip)

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:     "user",
						Remember:    false,
						RememberFor: 0,
						Acr:         "1",
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetConsentRequest(r.URL.Query().Get("consent_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.False(t, rr.Skip)

					v, res, err := apiClient.AcceptConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.AcceptConsentRequest{
						GrantScope:  []string{"scope-a"},
						Remember:    false,
						RememberFor: 0,
						Session: swagger.ConsentRequestSession{
							AccessToken: map[string]interface{}{"foo": "bar"},
							IdToken:     map[string]interface{}{"bar": "baz"},
						},
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusOK,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
			expectErr:             []bool{true, true, false},
		},
		{
			d:                     "This should fail because id_token_hint does not match authentication session and prompt is none",
			req:                   fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar:                   persistentCJ,
			prompt:                "none",
			idTokenHint:           fooUserIDToken,
			expectFinalStatusCode: fosite.ErrLoginRequired.StatusCode(),
			expectErrType:         []error{fosite.ErrLoginRequired},
			expectErr:             []bool{true},
		},
		{
			d:           "This should pass and require authentication because id_token_hint does not match subject from session",
			req:         fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar:         persistentCJ,
			idTokenHint: fooUserIDToken,
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.False(t, rr.Skip)

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:     "not-foouser",
						Remember:    false,
						RememberFor: 0,
						Acr:         "1",
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: fosite.ErrLoginRequired.StatusCode(),
			expectErrType:         []error{ErrAbortOAuth2Request, fosite.ErrLoginRequired},
			expectErr:             []bool{true, true},
		},
		{
			d:           "This should pass and require authentication because id_token_hint does not match subject from session",
			req:         fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"code"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar:         persistentCJ,
			idTokenHint: fooUserIDToken,
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.False(t, rr.Skip)

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:     "foouser",
						Remember:    false,
						RememberFor: 0,
						Acr:         "1",
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetConsentRequest(r.URL.Query().Get("consent_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.False(t, rr.Skip)

					v, res, err := apiClient.AcceptConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.AcceptConsentRequest{
						GrantScope:  []string{"scope-a"},
						Remember:    false,
						RememberFor: 0,
						Session: swagger.ConsentRequestSession{
							AccessToken: map[string]interface{}{"foo": "bar"},
							IdToken:     map[string]interface{}{"bar": "baz"},
						},
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					require.NotEmpty(t, v.RedirectTo)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusOK,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
			expectErr:             []bool{true, true, false},
			expectSession: &HandledConsentRequest{
				ConsentRequest: &ConsentRequest{Subject: "foouser"},
				GrantedScope:   []string{"scope-a"},
				Remember:       false,
				RememberFor:    0,
				Session: &ConsentRequestSessionData{
					AccessToken: map[string]interface{}{"foo": "bar"},
					IDToken:     map[string]interface{}{"bar": "baz"},
				},
			},
		},

		// checks revoking sessions
		{
			d:   "This should pass as regularly and create a new session",
			req: fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"token", "code", "id_token"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar: persistentCJ2,
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					v, _, _ := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:  "auth-user",
						Remember: true,
					})
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					v, _, _ := apiClient.AcceptConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.AcceptConsentRequest{GrantScope: []string{"scope-a"}})
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusOK,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
			expectErr:             []bool{true, true, false},
		},
		{
			d:      "This should pass and also revoke the session cookie",
			req:    fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"token", "code", "id_token"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar:    persistentCJ2,
			prompt: "login",
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					v, _, _ := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:  "not-auth-user",
						Remember: false,
					})
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					v, _, _ := apiClient.AcceptConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.AcceptConsentRequest{GrantScope: []string{"scope-a"}})
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusOK,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
			expectErr:             []bool{true, true, false},
		}, // these two tests depend on one another
		{
			d:   "This should require re-authentication because the session was revoked in the previous test",
			req: fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"token", "code", "id_token"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar: persistentCJ2,
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.False(t, rr.Skip)
					assert.Empty(t, "", rr.Subject)

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:  "foouser",
						Remember: true,
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			cph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					v, _, _ := apiClient.AcceptConsentRequest(r.URL.Query().Get("consent_challenge"), swagger.AcceptConsentRequest{GrantScope: []string{"scope-a"}})
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusOK,
			expectErrType:         []error{ErrAbortOAuth2Request, ErrAbortOAuth2Request, nil},
			expectErr:             []bool{true, true, false},
		},
		{
			d:           "This should fail because the user from the ID token does not match the user from the accept login request",
			req:         fosite.AuthorizeRequest{ResponseTypes: fosite.Arguments{"token", "code", "id_token"}, Request: fosite.Request{Client: &client.Client{ID: "client-id"}, Scopes: []string{"scope-a"}}},
			jar:         newCookieJar(),
			idTokenHint: fooUserIDToken,
			lph: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					rr, res, err := apiClient.GetLoginRequest(r.URL.Query().Get("login_challenge"))
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					assert.False(t, rr.Skip)
					assert.EqualValues(t, "", rr.Subject)
					assert.EqualValues(t, "foouser", rr.OidcContext.IdTokenHintClaims["sub"])

					v, res, err := apiClient.AcceptLoginRequest(r.URL.Query().Get("login_challenge"), swagger.AcceptLoginRequest{
						Subject:  "not-foouser",
						Remember: false,
					})
					require.NoError(t, err)
					require.EqualValues(t, http.StatusOK, res.StatusCode)
					http.Redirect(w, r, v.RedirectTo, http.StatusFound)
				}
			},
			expectFinalStatusCode: http.StatusBadRequest,
			expectErrType:         []error{ErrAbortOAuth2Request, fosite.ErrLoginRequired},
			expectErr:             []bool{true, true},
		},
	} {
		t.Run(fmt.Sprintf("case=%d/description=%s", k, tc.d), func(t *testing.T) {
			if tc.setup != nil {
				tc.setup()
			}

			if tc.lph != nil {
				lph = tc.lph(t)
			} else {
				lph = noopHandler(t)
			}

			if tc.cph != nil {
				cph = tc.cph(t)
			} else {
				cph = noopHandler(t)
			}

			calls := -1
			aph = func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.True(t, len(tc.expectErrType) >= calls+1, "%d (expect) < %d (got)", len(tc.expectErrType), calls+1)
				require.True(t, len(tc.expectErr) >= calls+1, "%d (expect) < %d (got)", len(tc.expectErr), calls+1)
				require.NoError(t, r.ParseForm())
				tc.req.Form = r.Form

				c, err := strategy.HandleOAuth2AuthorizationRequest(w, r, &tc.req)
				t.Logf("DefaultStrategy returned at call %d:\n\tgot: %+v\n\texpected: %s", calls, c, err)

				if tc.expectErr[calls] {
					assert.Error(t, err)
					if tc.expectErrType[calls] != nil {
						assert.EqualError(t, tc.expectErrType[calls], err.Error(), "%+v", err)
					}
				} else {
					require.NoError(t, err)
					if tc.expectSession != nil {
						require.NotNil(t, c)
						assert.EqualValues(t, tc.expectSession.GrantedScope, c.GrantedScope)
						assert.EqualValues(t, tc.expectSession.Remember, c.Remember)
						assert.EqualValues(t, tc.expectSession.RememberFor, c.RememberFor)
						assert.EqualValues(t, tc.expectSession.ConsentRequest.Subject, c.ConsentRequest.Subject)
					}
				}

				if errors.Cause(err) == ErrAbortOAuth2Request {
					// nothing to do, indicates redirect
				} else if err != nil {
					writer.WriteError(w, r, err)
				} else {
					writer.Write(w, r, c)
				}
			}

			cl := &http.Client{
				Jar: tc.jar,
			}
			resp, err := cl.Get(
				ap.URL + "?" +
					"login_verifier=" + tc.lv + "&" +
					"consent_verifier=" + tc.cv + "&" +
					"prompt=" + tc.prompt + "&" +
					"max_age=" + tc.maxAge + "&" +
					"id_token_hint=" + tc.idTokenHint + "&",
			)
			require.NoError(t, err)
			out, err := ioutil.ReadAll(resp.Body)
			require.NoError(t, err)
			resp.Body.Close()
			assert.EqualValues(t, tc.expectFinalStatusCode, resp.StatusCode, "%s\n%s", resp.Request.URL.String(), out)
			//assert.Empty(t, resp.Request.URL.String())
		})
	}
}
