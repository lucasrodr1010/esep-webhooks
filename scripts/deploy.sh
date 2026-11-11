#!/usr/bin/env bash
# Deploy build/function.zip to AWS Lambda (us-east-2) as EsepWebhook.
# Needs: `aws configure` done (spec, "Set up AWS credentials", page 3).
# This is the Go stand-in for the spec's `dotnet lambda deploy-function EsepWebhook`.
#
# First deploy:  ROLE_ARN=arn:aws:iam::<account>:role/EsepWebhook-role bash scripts/deploy.sh
# Later deploys: bash scripts/deploy.sh   (updates the code; role not needed)
#
# This script NEVER touches the SLACK_URL environment variable. Set it by hand
# in the console (spec Part 2, page 4) and confirm it is still there after each
# deploy (spec Part 5, page 10, step 2). update-function-code leaves it alone.
set -euo pipefail
cd "$(dirname "$0")/.."

FUNCTION=EsepWebhook
REGION=us-east-2
bash scripts/build.sh

if aws lambda get-function --function-name "$FUNCTION" --region "$REGION" >/dev/null 2>&1; then
  aws lambda update-function-code --function-name "$FUNCTION" --region "$REGION" \
    --zip-file fileb://build/function.zip >/dev/null
  echo "updated $FUNCTION"
else
  : "${ROLE_ARN:?first deploy needs ROLE_ARN=<arn of a role with AWSLambdaBasicExecutionRole>}"
  aws lambda create-function --function-name "$FUNCTION" --region "$REGION" \
    --runtime provided.al2023 --architectures arm64 --handler bootstrap \
    --role "$ROLE_ARN" --timeout 10 --zip-file fileb://build/function.zip >/dev/null
  echo "created $FUNCTION"
fi
echo "Now: Lambda console -> $FUNCTION -> Configuration -> Environment variables: confirm SLACK_URL is set."
