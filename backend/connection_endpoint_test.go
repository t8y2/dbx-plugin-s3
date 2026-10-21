package main

import (
	"strings"
	"testing"
)

// Placeholder values for the in-process fake S3 server; never real credentials.
var (
	testEndpointAccessKey = "access" + "-key"
	testEndpointSecretKey = "secret" + "-key"
)

func endpointTestConnection(endpoint string) map[string]any {
	return map[string]any{
		"connection": map[string]any{
			"id":                 "connection-1",
			"database":           "example-bucket",
			"username":           testEndpointAccessKey,
			"connection_secrets": map[string]any{"secret_key": testEndpointSecretKey},
			"external_config":    map[string]any{"endpoint": endpoint, "addressing_style": "path"},
		},
	}
}

func TestParseConnectionTrimsInvisibleCharactersFromEndpoint(test *testing.T) {
	for _, testCase := range []struct {
		name     string
		endpoint string
	}{
		{name: "trailing space", endpoint: "oss-cn-hangzhou.aliyuncs.com "},
		{name: "leading newline", endpoint: "\noss-cn-hangzhou.aliyuncs.com"},
		{name: "zero-width space", endpoint: "oss-cn-hangzhou.aliyuncs.com\u200b"},
		{name: "ideographic space", endpoint: "oss-cn-hangzhou.aliyuncs.com\u3000"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			config, pluginError := parseConnection(endpointTestConnection(testCase.endpoint))
			if pluginError != nil {
				test.Fatalf("trimmed endpoint must pass validation: %v", pluginError.Message)
			}
			if config.endpoint != "https://oss-cn-hangzhou.aliyuncs.com" {
				test.Fatalf("unexpected endpoint: %q", config.endpoint)
			}
		})
	}
}

func TestParseConnectionEndpointErrorEchoesReceivedValue(test *testing.T) {
	_, pluginError := parseConnection(endpointTestConnection("example.com/bucket"))
	if pluginError == nil || !strings.Contains(pluginError.Message, `got "https://example.com/bucket"`) {
		test.Fatalf("endpoint error must echo the received value: %#v", pluginError)
	}
}
