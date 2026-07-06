package openai

import "fmt"

func normalizeStrictSchema(schema any) (any, error) {
	cleaned := cleanStrictSchema(schema)
	if err := validateStrictSchema(cleaned, "schema"); err != nil {
		return nil, err
	}
	return cleaned, nil
}

func cleanStrictSchema(schema any) any {
	switch s := schema.(type) {
	case map[string]any:
		out := make(map[string]any, len(s))
		for key, value := range s {
			if key == "examples" || key == "default" || key == "const" {
				continue
			}
			out[key] = cleanStrictSchema(value)
		}
		if schemaTypeIncludesObject(out["type"]) {
			if _, ok := out["additionalProperties"]; !ok {
				out["additionalProperties"] = false
			}
		}
		return out
	case []any:
		out := make([]any, len(s))
		for i, value := range s {
			out[i] = cleanStrictSchema(value)
		}
		return out
	default:
		return schema
	}
}

func validateStrictSchema(schema any, path string) error {
	switch s := schema.(type) {
	case map[string]any:
		if schemaTypeIncludesObject(s["type"]) {
			if v, ok := s["additionalProperties"]; !ok || v != false {
				return fmt.Errorf("%s: object schemas used with strict=true require additionalProperties:false", path)
			}
			props, _ := s["properties"].(map[string]any)
			if len(props) > 0 {
				required := requiredFieldSet(s["required"])
				for name := range props {
					if _, ok := required[name]; !ok {
						return fmt.Errorf("%s.properties.%s must be listed in required when strict=true", path, name)
					}
				}
			}
		}
		for key, value := range s {
			if err := validateStrictSchema(value, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for i, value := range s {
			if err := validateStrictSchema(value, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func schemaTypeIncludesObject(value any) bool {
	switch v := value.(type) {
	case string:
		return v == "object"
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == "object" {
				return true
			}
		}
	case []string:
		for _, item := range v {
			if item == "object" {
				return true
			}
		}
	}
	return false
}

func requiredFieldSet(value any) map[string]struct{} {
	out := make(map[string]struct{})
	switch required := value.(type) {
	case []any:
		for _, item := range required {
			if s, ok := item.(string); ok {
				out[s] = struct{}{}
			}
		}
	case []string:
		for _, item := range required {
			out[item] = struct{}{}
		}
	}
	return out
}
