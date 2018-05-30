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

package jwk

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/ory/herodot"
	"github.com/pkg/errors"
	"github.com/square/go-jose"
)

const (
	IDTokenKeyName    = "hydra.openid.id-token"
	KeyHandlerPath    = "/keys"
	WellKnownKeysPath = "/.well-known/jwks.json"
)

type Handler struct {
	Manager    Manager
	Generators map[string]KeyGenerator
	H          herodot.Writer
}

func (h *Handler) GetGenerators() map[string]KeyGenerator {
	if h.Generators == nil || len(h.Generators) == 0 {
		h.Generators = map[string]KeyGenerator{
			"RS256": &RS256Generator{},
			"ES512": &ECDSA512Generator{},
			"HS256": &HS256Generator{},
			"HS512": &HS512Generator{},
		}
	}
	return h.Generators
}

func (h *Handler) SetRoutes(r *httprouter.Router) {
	r.GET(WellKnownKeysPath, h.WellKnown)
	r.GET(KeyHandlerPath+"/:set/:key", h.GetKey)
	r.GET(KeyHandlerPath+"/:set", h.GetKeySet)

	r.POST(KeyHandlerPath+"/:set", h.Create)

	r.PUT(KeyHandlerPath+"/:set/:key", h.UpdateKey)
	r.PUT(KeyHandlerPath+"/:set", h.UpdateKeySet)

	r.DELETE(KeyHandlerPath+"/:set/:key", h.DeleteKey)
	r.DELETE(KeyHandlerPath+"/:set", h.DeleteKeySet)
}

// swagger:model jsonWebKeySetGeneratorRequest
type createRequest struct {
	// The algorithm to be used for creating the key. Supports "RS256", "ES512", "HS512", and "HS256"
	// required: true
	// in: body
	Algorithm string `json:"alg"`

	// The kid of the key to be created
	// required: true
	// in: body
	KeyID string `json:"kid"`
}

type joseWebKeySetRequest struct {
	Keys []json.RawMessage `json:"keys"`
}

// swagger:route GET /.well-known/jwks.json oAuth2 wellKnown
//
// Get Well-Known JSON Web Keys
//
// Returns metadata for discovering important JSON Web Keys. Currently, this endpoint returns the public key for verifying OpenID Connect ID Tokens.
//
// A JSON Web Key (JWK) is a JavaScript Object Notation (JSON) data structure that represents a cryptographic key. A JWK Set is a JSON data structure that represents a set of JWKs. A JSON Web Key is identified by its set and key id. ORY Hydra uses this functionality to store cryptographic keys used for TLS and JSON Web Tokens (such as OpenID Connect ID tokens), and allows storing user-defined keys as well.
//
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Responses:
//       200: jsonWebKeySet
//       401: genericError
//       403: genericError
//       500: genericError
func (h *Handler) WellKnown(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	keys, err := h.Manager.GetKeySet(IDTokenKeyName)
	if err != nil {
		h.H.WriteError(w, r, err)
		return
	}

	keys, err = FindKeysByPrefix(keys, "public")
	if err != nil {
		h.H.WriteError(w, r, err)
		return
	}

	h.H.Write(w, r, keys)
}

// swagger:route GET /keys/{set}/{kid} jsonWebKey getJsonWebKey
//
// Retrieve a JSON Web Key
//
// This endpoint can be used to retrieve JWKs stored in ORY Hydra.
//
// A JSON Web Key (JWK) is a JavaScript Object Notation (JSON) data structure that represents a cryptographic key. A JWK Set is a JSON data structure that represents a set of JWKs. A JSON Web Key is identified by its set and key id. ORY Hydra uses this functionality to store cryptographic keys used for TLS and JSON Web Tokens (such as OpenID Connect ID tokens), and allows storing user-defined keys as well.
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Responses:
//       200: jsonWebKeySet
//       401: genericError
//       403: genericError
//       500: genericError
func (h *Handler) GetKey(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var setName = ps.ByName("set")
	var keyName = ps.ByName("key")

	keys, err := h.Manager.GetKey(setName, keyName)
	if err != nil {
		h.H.WriteError(w, r, err)
		return
	}

	h.H.Write(w, r, keys)
}

// swagger:route GET /keys/{set} jsonWebKey getJsonWebKeySet
//
// Retrieve a JSON Web Key Set
//
// This endpoint can be used to retrieve JWK Sets stored in ORY Hydra.
//
// A JSON Web Key (JWK) is a JavaScript Object Notation (JSON) data structure that represents a cryptographic key. A JWK Set is a JSON data structure that represents a set of JWKs. A JSON Web Key is identified by its set and key id. ORY Hydra uses this functionality to store cryptographic keys used for TLS and JSON Web Tokens (such as OpenID Connect ID tokens), and allows storing user-defined keys as well.
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Responses:
//       200: jsonWebKeySet
//       401: genericError
//       403: genericError
//       500: genericError
func (h *Handler) GetKeySet(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var setName = ps.ByName("set")

	keys, err := h.Manager.GetKeySet(setName)
	if err != nil {
		h.H.WriteError(w, r, err)
		return
	}

	h.H.Write(w, r, keys)
}

// swagger:route POST /keys/{set} jsonWebKey createJsonWebKeySet
//
// Generate a new JSON Web Key
//
// This endpoint is capable of generating JSON Web Key Sets for you. There a different strategies available, such as symmetric cryptographic keys (HS256, HS512) and asymetric cryptographic keys (RS256, ECDSA). If the specified JSON Web Key Set does not exist, it will be created.
//
// A JSON Web Key (JWK) is a JavaScript Object Notation (JSON) data structure that represents a cryptographic key. A JWK Set is a JSON data structure that represents a set of JWKs. A JSON Web Key is identified by its set and key id. ORY Hydra uses this functionality to store cryptographic keys used for TLS and JSON Web Tokens (such as OpenID Connect ID tokens), and allows storing user-defined keys as well.
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Responses:
//       200: jsonWebKeySet
//       401: genericError
//       403: genericError
//       500: genericError
func (h *Handler) Create(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var keyRequest createRequest
	var set = ps.ByName("set")

	if err := json.NewDecoder(r.Body).Decode(&keyRequest); err != nil {
		h.H.WriteError(w, r, errors.WithStack(err))
	}

	generator, found := h.GetGenerators()[keyRequest.Algorithm]
	if !found {
		h.H.WriteErrorCode(w, r, http.StatusBadRequest, errors.Errorf("Generator %s unknown", keyRequest.Algorithm))
		return
	}

	keys, err := generator.Generate(keyRequest.KeyID)
	if err != nil {
		h.H.WriteError(w, r, err)
		return
	}

	if err := h.Manager.AddKeySet(set, keys); err != nil {
		h.H.WriteError(w, r, err)
		return
	}

	h.H.WriteCreated(w, r, fmt.Sprintf("%s://%s/keys/%s", r.URL.Scheme, r.URL.Host, set), keys)
}

// swagger:route PUT /keys/{set} jsonWebKey updateJsonWebKeySet
//
// Update a JSON Web Key Set
//
// Use this method if you do not want to let Hydra generate the JWKs for you, but instead save your own.
//
// A JSON Web Key (JWK) is a JavaScript Object Notation (JSON) data structure that represents a cryptographic key. A JWK Set is a JSON data structure that represents a set of JWKs. A JSON Web Key is identified by its set and key id. ORY Hydra uses this functionality to store cryptographic keys used for TLS and JSON Web Tokens (such as OpenID Connect ID tokens), and allows storing user-defined keys as well.
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Responses:
//       200: jsonWebKeySet
//       401: genericError
//       403: genericError
//       500: genericError
func (h *Handler) UpdateKeySet(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var requests joseWebKeySetRequest
	var keySet = new(jose.JSONWebKeySet)
	var set = ps.ByName("set")

	if err := json.NewDecoder(r.Body).Decode(&requests); err != nil {
		h.H.WriteError(w, r, errors.WithStack(err))
		return
	}

	for _, request := range requests.Keys {
		key := &jose.JSONWebKey{}
		if err := key.UnmarshalJSON(request); err != nil {
			h.H.WriteError(w, r, errors.WithStack(err))
		}
		keySet.Keys = append(keySet.Keys, *key)
	}

	if err := h.Manager.AddKeySet(set, keySet); err != nil {
		h.H.WriteError(w, r, err)
		return
	}

	h.H.Write(w, r, keySet)
}

// swagger:route PUT /keys/{set}/{kid} jsonWebKey updateJsonWebKey
//
// Update a JSON Web Key
//
// Use this method if you do not want to let Hydra generate the JWKs for you, but instead save your own.
//
// A JSON Web Key (JWK) is a JavaScript Object Notation (JSON) data structure that represents a cryptographic key. A JWK Set is a JSON data structure that represents a set of JWKs. A JSON Web Key is identified by its set and key id. ORY Hydra uses this functionality to store cryptographic keys used for TLS and JSON Web Tokens (such as OpenID Connect ID tokens), and allows storing user-defined keys as well.
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Responses:
//       200: jsonWebKey
//       401: genericError
//       403: genericError
//       500: genericError
func (h *Handler) UpdateKey(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var key jose.JSONWebKey
	var set = ps.ByName("set")

	if err := json.NewDecoder(r.Body).Decode(&key); err != nil {
		h.H.WriteError(w, r, errors.WithStack(err))
		return
	}

	if err := h.Manager.AddKey(set, &key); err != nil {
		h.H.WriteError(w, r, err)
		return
	}

	h.H.Write(w, r, key)
}

// swagger:route DELETE /keys/{set} jsonWebKey deleteJsonWebKeySet
//
// Delete a JSON Web Key Set
//
// Use this endpoint to delete a complete JSON Web Key Set and all the keys in that set.
//
// A JSON Web Key (JWK) is a JavaScript Object Notation (JSON) data structure that represents a cryptographic key. A JWK Set is a JSON data structure that represents a set of JWKs. A JSON Web Key is identified by its set and key id. ORY Hydra uses this functionality to store cryptographic keys used for TLS and JSON Web Tokens (such as OpenID Connect ID tokens), and allows storing user-defined keys as well.
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Responses:
//       204: emptyResponse
//       401: genericError
//       403: genericError
//       500: genericError
func (h *Handler) DeleteKeySet(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var setName = ps.ByName("set")

	if err := h.Manager.DeleteKeySet(setName); err != nil {
		h.H.WriteError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// swagger:route DELETE /keys/{set}/{kid} jsonWebKey deleteJsonWebKey
//
// Delete a JSON Web Key
//
// Use this endpoint to delete a single JSON Web Key.
//
// A JSON Web Key (JWK) is a JavaScript Object Notation (JSON) data structure that represents a cryptographic key. A JWK Set is a JSON data structure that represents a set of JWKs. A JSON Web Key is identified by its set and key id. ORY Hydra uses this functionality to store cryptographic keys used for TLS and JSON Web Tokens (such as OpenID Connect ID tokens), and allows storing user-defined keys as well.
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Responses:
//       204: emptyResponse
//       401: genericError
//       403: genericError
//       500: genericError
func (h *Handler) DeleteKey(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var setName = ps.ByName("set")
	var keyName = ps.ByName("key")

	if err := h.Manager.DeleteKey(setName, keyName); err != nil {
		h.H.WriteError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
