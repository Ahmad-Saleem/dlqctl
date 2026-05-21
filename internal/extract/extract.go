package extract

import (
	"encoding/json"
	"fmt"
)

var defaultFields = []string{"requestId", "correlationId", "traceId"}

func Field(body string, field string) (string, error) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return "", fmt.Errorf("message body is not valid JSON: %w", err)
	}

	if field != "" {
		val, ok := data[field]
		if !ok {
			return "", fmt.Errorf("field %q not found in message body", field)
		}
		return fmt.Sprintf("%v", val), nil
	}

	for _, f := range defaultFields {
		if val, ok := data[f]; ok {
			return fmt.Sprintf("%v", val), nil
		}
	}

	return "", fmt.Errorf("no known trace field found (tried: %v)", defaultFields)
}
