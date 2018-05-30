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

package config

import (
	"plugin"

	"github.com/jmoiron/sqlx"
	"github.com/ory/fosite"
	"github.com/ory/hydra/client"
	"github.com/ory/hydra/consent"
	"github.com/ory/hydra/jwk"
	"github.com/ory/hydra/pkg"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type PluginConnection struct {
	Config     *Config
	plugin     *plugin.Plugin
	didConnect bool
	Logger     logrus.FieldLogger
	db         *sqlx.DB
}

func (c *PluginConnection) load() error {
	if c.plugin != nil {
		return nil
	}

	cf := c.Config
	p, err := plugin.Open(cf.DatabasePlugin)
	if err != nil {
		return errors.WithStack(err)
	}

	c.plugin = p
	return nil
}

func (c *PluginConnection) Ping() error {
	return c.db.Ping()
}

func (c *PluginConnection) Connect() error {
	cf := c.Config
	if c.didConnect {
		return nil
	}

	if err := c.load(); err != nil {
		return errors.WithStack(err)
	}

	if l, err := c.plugin.Lookup("Connect"); err != nil {
		return errors.Wrap(err, "Unable to look up `Connect`")
	} else if con, ok := l.(func(url string) (*sqlx.DB, error)); !ok {
		return errors.New("Unable to type assert `Connect`")
	} else {
		if db, err := con(cf.DatabaseURL); err != nil {
			return errors.Wrap(err, "Could not connect to database")
		} else {
			cf.GetLogger().Info("Successfully connected through database plugin")
			c.db = db
			cf.GetLogger().Debugf("Address of database plugin is: %s", c.db)
			if err := db.Ping(); err != nil {
				cf.GetLogger().WithError(err).Fatal("Could not ping database connection from plugin")
			}
		}
	}
	return nil
}

func (c *PluginConnection) NewClientManager() (client.Manager, error) {
	if err := c.load(); err != nil {
		return nil, errors.WithStack(err)
	}

	ctx := c.Config.Context()
	if l, err := c.plugin.Lookup("NewClientManager"); err != nil {
		return nil, errors.Wrap(err, "Unable to look up `NewClientManager`")
	} else if m, ok := l.(func(*sqlx.DB, fosite.Hasher) client.Manager); !ok {
		return nil, errors.New("Unable to type assert `NewClientManager`")
	} else {
		return m(c.db, ctx.Hasher), nil
	}
}

func (c *PluginConnection) NewJWKManager() (jwk.Manager, error) {
	if err := c.load(); err != nil {
		return nil, errors.WithStack(err)
	}

	if l, err := c.plugin.Lookup("NewJWKManager"); err != nil {
		return nil, errors.Wrap(err, "Unable to look up `NewJWKManager`")
	} else if m, ok := l.(func(*sqlx.DB, *jwk.AEAD) jwk.Manager); !ok {
		return nil, errors.New("Unable to type assert `NewJWKManager`")
	} else {
		return m(c.db, &jwk.AEAD{
			Key: c.Config.GetSystemSecret(),
		}), nil
	}
}

func (c *PluginConnection) NewOAuth2Manager(clientManager client.Manager) (pkg.FositeStorer, error) {
	if err := c.load(); err != nil {
		return nil, errors.WithStack(err)
	}

	if l, err := c.plugin.Lookup("NewOAuth2Manager"); err != nil {
		return nil, errors.Wrap(err, "Unable to look up `NewOAuth2Manager`")
	} else if m, ok := l.(func(*sqlx.DB, client.Manager, logrus.FieldLogger) pkg.FositeStorer); !ok {
		return nil, errors.New("Unable to type assert `NewOAuth2Manager`")
	} else {
		return m(c.db, clientManager, c.Config.GetLogger()), nil
	}
}

func (c *PluginConnection) NewConsentManager() (consent.Manager, error) {
	if err := c.load(); err != nil {
		return nil, errors.WithStack(err)
	}

	if l, err := c.plugin.Lookup("NewConsentManager"); err != nil {
		return nil, errors.Wrap(err, "Unable to look up `NewConsentManager`")
	} else if m, ok := l.(func(*sqlx.DB) consent.Manager); !ok {
		return nil, errors.Errorf("Unable to type assert `NewConsentManager`, got %v", l)
	} else {
		return m(c.db), nil
	}
}
