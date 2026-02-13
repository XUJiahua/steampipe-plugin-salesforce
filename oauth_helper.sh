#!/bin/bash
# OAuth Authorization Code Flow with PKCE
# Usage: bash oauth_helper.sh
# Requires: SALESFORCE_URL, SALESFORCE_CLIENT_ID, SALESFORCE_CLIENT_SECRET in .env or environment
set -euo pipefail
if [ -f .env ]; then set -a; source .env; set +a; fi

SF_INSTANCE="${SALESFORCE_URL:?Set SALESFORCE_URL in .env}"
CLIENT_ID="${SALESFORCE_CLIENT_ID:?Set SALESFORCE_CLIENT_ID in .env}"
CLIENT_SECRET="${SALESFORCE_CLIENT_SECRET:?Set SALESFORCE_CLIENT_SECRET in .env}"
REDIRECT_URI="http://localhost:1717/OauthRedirect"

# Step 1: Generate PKCE code_verifier and code_challenge
CODE_VERIFIER=$(openssl rand -base64 32 | tr -d '=+/' | cut -c1-43)
CODE_CHALLENGE=$(printf '%s' "$CODE_VERIFIER" | openssl dgst -sha256 -binary | base64 | tr '+/' '-_' | tr -d '=')

echo "=== Step 1: Open this URL in your browser ==="
echo ""
echo "${SF_INSTANCE}/services/oauth2/authorize?response_type=code&client_id=${CLIENT_ID}&redirect_uri=$(python3 -c "import urllib.parse; print(urllib.parse.quote('${REDIRECT_URI}', safe=''))")&scope=api%20refresh_token&code_challenge=${CODE_CHALLENGE}&code_challenge_method=S256"
echo ""
echo "=== Step 2: After login, browser will redirect to localhost:1717 (page won't load, that's OK) ==="
echo "=== Copy the 'code' parameter from the URL bar ==="
echo "=== Example: http://localhost:1717/OauthRedirect?code=aPrx...XXXX ==="
echo ""
read -p "Paste the authorization code here: " AUTH_CODE

if [ -z "$AUTH_CODE" ]; then
    echo "Error: no authorization code provided"
    exit 1
fi

if [ -z "$CLIENT_SECRET" ]; then
    read -p "Enter your Consumer Secret: " CLIENT_SECRET
fi

# Step 3: Exchange code for tokens
echo ""
echo "=== Step 3: Exchanging code for tokens... ==="
RESPONSE=$(curl -s -X POST "${SF_INSTANCE}/services/oauth2/token" \
    -d "grant_type=authorization_code" \
    -d "code=${AUTH_CODE}" \
    -d "client_id=${CLIENT_ID}" \
    -d "client_secret=${CLIENT_SECRET}" \
    -d "redirect_uri=${REDIRECT_URI}" \
    -d "code_verifier=${CODE_VERIFIER}")

echo ""
echo "=== Response ==="
echo "$RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$RESPONSE"

# Extract tokens
ACCESS_TOKEN=$(echo "$RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('access_token',''))" 2>/dev/null)
REFRESH_TOKEN=$(echo "$RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('refresh_token',''))" 2>/dev/null)
INSTANCE_URL=$(echo "$RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('instance_url',''))" 2>/dev/null)

if [ -n "$REFRESH_TOKEN" ]; then
    echo ""
    echo "=== Success! Add these to your .env ==="
    echo "SALESFORCE_URL=${INSTANCE_URL}"
    echo "SALESFORCE_CLIENT_ID=${CLIENT_ID}"
    echo "SALESFORCE_CLIENT_SECRET=${CLIENT_SECRET}"
    echo "SALESFORCE_REFRESH_TOKEN=${REFRESH_TOKEN}"
fi
