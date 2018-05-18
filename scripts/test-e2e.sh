#!/usr/bin/env bash

set -euo pipefail

cd "$( dirname "${BASH_SOURCE[0]}" )/.."

DATABASE_URL=memory hydra serve --dangerous-force-http --disable-telemetry &
HYDRA_URL=http://localhost:4444 PORT=3000 LOGIN=accept CONSENT=accept mock-lcp &
HYDRA_URL=http://localhost:4444 PORT=3001 LOGIN=accept CONSENT=reject mock-lcp &
HYDRA_URL=http://localhost:4444 PORT=3002 LOGIN=reject CONSENT=reject mock-lcp &

while ! echo exit | nc 127.0.0.1 4444; do sleep 1; done
while ! echo exit | nc 127.0.0.1 3000; do sleep 1; done
while ! echo exit | nc 127.0.0.1 3001; do sleep 1; done
while ! echo exit | nc 127.0.0.1 3002; do sleep 1; done

export HYDRA_URL=http://localhost:4444/
export OAUTH2_CLIENT_ID=foobar
export OAUTH2_CLIENT_SECRET=bazbar

hydra clients create --id $OAUTH2_CLIENT_ID --secret $OAUTH2_CLIENT_SECRET -g client_credentials
token=$(hydra token client)
hydra token introspect $token
hydra clients delete foobar

hydra clients create \
    --endpoint http://localhost:4444 \
    --id test-client \
    --secret test-secret \
    --response-types code,id_token \
    --grant-types refresh_token,authorization_code \
    --scope openid,offline \
    --callbacks http://127.0.0.1:4445/callback

hydra token user \
    --endpoint http://localhost:4444/ \
    --scope openid,offline \
    --client-id test-client \
    --client-secret test-secret &

sleep 5

curl http://127.0.0.1:4445