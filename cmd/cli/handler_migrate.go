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

package cli

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/ory/hydra/client"
	"github.com/ory/hydra/config"
	"github.com/ory/hydra/consent"
	"github.com/ory/hydra/jwk"
	"github.com/ory/hydra/oauth2"
	"github.com/ory/hydra/pkg"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type MigrateHandler struct {
	c *config.Config
}

func newMigrateHandler(c *config.Config) *MigrateHandler {
	return &MigrateHandler{c: c}
}

type schemaCreator interface {
	CreateSchemas() (int, error)
}

func (h *MigrateHandler) connectToSql(dsn string) (*sqlx.DB, error) {
	var db *sqlx.DB

	u, err := url.Parse(dsn)
	if err != nil {
		return nil, errors.Errorf("Could not parse DATABASE_URL: %s", err)
	}

	if err := pkg.Retry(h.c.GetLogger(), time.Second*15, time.Minute*2, func() error {
		if u.Scheme == "mysql" {
			dsn = strings.Replace(dsn, "mysql://", "", -1)
		}

		if db, err = sqlx.Open(u.Scheme, dsn); err != nil {
			return errors.Errorf("Could not connect to SQL: %s", err)
		} else if err := db.Ping(); err != nil {
			return errors.Errorf("Could not connect to SQL: %s", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return db, nil
}

func (h *MigrateHandler) MigrateSQL(cmd *cobra.Command, args []string) {
	if len(args) == 0 {
		fmt.Println(cmd.UsageString())
		return
	}

	db, err := h.connectToSql(args[0])
	if err != nil {
		fmt.Printf("An error occurred while connecting to SQL: %s", err)
		os.Exit(1)
		return
	}

	if err := h.runMigrateSQL(db); err != nil {
		fmt.Printf("An error occurred while running the migrations: %s", err)
		os.Exit(1)
		return
	}
	fmt.Println("Migration successful!")
}

func (h *MigrateHandler) runMigrateSQL(db *sqlx.DB) error {
	var total int
	for k, m := range map[string]schemaCreator{
		"client":  &client.SQLManager{DB: db},
		"oauth2":  &oauth2.FositeSQLStore{DB: db},
		"jwk":     &jwk.SQLManager{DB: db},
		"consent": consent.NewSQLManager(db, nil),
	} {
		fmt.Printf("Applying `%s` SQL migrations...\n", k)
		if num, err := m.CreateSchemas(); err != nil {
			return errors.Wrapf(err, "Could not apply `%s` SQL migrations", k)
		} else {
			fmt.Printf("Applied %d `%s` SQL migrations.\n", num, k)
			total += num
		}
	}

	fmt.Printf("Migration successful! Applied a total of %d SQL migrations.\n", total)
	return nil
}
