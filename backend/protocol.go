package main

import (
	"context"
	"encoding/base64"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
)

func requestConnectionID(values map[string]any) (string, *dbxpluginsdk.PluginError) {
	connection, _ := values["connection"].(map[string]any)
	connectionID := stringValue(connection["id"])
	if connectionID == "" {
		connectionID = stringValue(values["connectionId"])
	}
	if connectionID == "" {
		return "", invalidParams("Missing connection id")
	}
	return connectionID, nil
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func numberValue(value any) int {
	result, _ := value.(float64)
	return int(result)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func encodeCursor(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeCursor(value string) string {
	if value == "" {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return value
	}
	return string(decoded)
}

func invalidParams(message string) *dbxpluginsdk.PluginError {
	return dbxpluginsdk.NewError(-32602, message)
}

func remoteError(message string) *dbxpluginsdk.PluginError {
	return dbxpluginsdk.NewError(-32010, message)
}

func operationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), operationTimeout)
}
