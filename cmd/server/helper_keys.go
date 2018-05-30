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

package server

import (
	"crypto/ecdsa"
	"crypto/rsa"

	"github.com/ory/hydra/config"
	"github.com/ory/hydra/jwk"
	"github.com/ory/hydra/pkg"
	"github.com/pkg/errors"
	"github.com/square/go-jose"
)

func createOrGetJWK(c *config.Config, set string, prefix string) (key *jose.JSONWebKey, err error) {
	ctx := c.Context()

	keys, err := ctx.KeyManager.GetKeySet(set)
	if errors.Cause(err) == pkg.ErrNotFound || keys != nil && len(keys.Keys) == 0 {
		c.GetLogger().Infof("JSON Web Key Set %s does not exist yet, generating new key pair...", set)
		keys, err = createJWKS(ctx, set)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	key, err = jwk.FindKeyByPrefix(keys, prefix)
	if err != nil {
		c.GetLogger().Infof("JSON Web Key with prefix %s not found in JSON Web Key Set %s, generating new key pair...", prefix, set)

		keys, err = createJWKS(ctx, set)
		if err != nil {
			return nil, err
		}

		key, err = jwk.FindKeyByPrefix(keys, prefix)
		if err != nil {
			return nil, err
		}
	}

	return key, nil
}

func createJWKS(ctx *config.Context, set string) (*jose.JSONWebKeySet, error) {
	generator := jwk.RS256Generator{}
	keys, err := generator.Generate("")
	if err != nil {
		return nil, errors.Wrapf(err, "Could not generate %s key", set)
	}

	for i, k := range keys.Keys {
		k.Use = "sig"
		keys.Keys[i] = k
	}

	err = ctx.KeyManager.AddKeySet(set, keys)
	if err != nil {
		return nil, errors.Wrapf(err, "Could not persist %s key", set)
	}

	return keys, nil
}

func publicKey(key interface{}) interface{} {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	default:
		return nil
	}
}
