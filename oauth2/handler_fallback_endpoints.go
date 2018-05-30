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
 * @copyright 	2015-2018 Aeneas Rekkas <aeneas+oss@aeneas.io>
 * @license 	Apache-2.0
 */

package oauth2

import (
	"fmt"
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func (h *Handler) DefaultConsentHandler(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	h.L.Warnln("It looks like no consent/login URL was set. All OAuth2 flows except client credentials will fail.")

	w.Write([]byte(`
<html>
<head>
	<title>Misconfigured consent/login URL</title>
</head>
<body>
<p>
	It looks like you forgot to set the consent/login provider url, which can be set using the <code>CONSENT_URL</code> and <code>LOGIN_URL</code>
	environment variable.
</p>
<p>
	If you are an administrator, please read <a href="https://www.ory.sh/docs">
	the guide</a> to understand what you need to do. If you are a user, please contact the administrator.
</p>
</body>
</html>
`))
}

func (h *Handler) DefaultErrorHandler(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	h.L.Warnln("It looks like no OAuth2 Error URL was set.")

	fmt.Fprintf(w, `
<html>
<head>
	<title>An OAuth 2.0 Error Occurred</title>
</head>
<body>
<h1>
	The OAuth2 request resulted in an error.
</h1>
<ul>
	<li>Error: %s</li>
	<li>Description: %s</li>
</ul>
<p>
	You are seeing this default error page because the administrator has not set a dedicated error URL. 
	If you are an administrator, please read <a href="https://www.ory.sh/docs">the guide</a> to understand what you
	need to do. If you are a user, please contact the administrator.
</p>
</body>
</html>
`, r.URL.Query().Get("error"), r.URL.Query().Get("error_description"))
}
