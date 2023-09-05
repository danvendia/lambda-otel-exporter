#!/bin/bash

GOOS=linux GOARCH=arm64 go build -o bin/extensions/lambda-otel-exporter main.go
cd bin
zip -r extension.zip extensions/
echo to publish run: aws lambda publish-layer-version \
 --layer-name "lambda-otel-exporter" \
 --region us-east-1 \
 --zip-file  "fileb://bin/extension.zip"