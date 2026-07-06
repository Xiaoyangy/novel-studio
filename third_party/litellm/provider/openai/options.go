package openai

import (
	"fmt"
)

const (
	ProviderOptionFrequencyPenalty     = "frequency_penalty"
	ProviderOptionPresencePenalty      = "presence_penalty"
	ProviderOptionLogitBias            = "logit_bias"
	ProviderOptionN                    = "n"
	ProviderOptionLogprobs             = "logprobs"
	ProviderOptionTopLogprobs          = "top_logprobs"
	ProviderOptionStore                = "store"
	ProviderOptionModeration           = "moderation"
	ProviderOptionStreamOptions        = "stream_options"
	ProviderOptionPromptCacheKey       = "prompt_cache_key"
	ProviderOptionPromptCacheRetention = "prompt_cache_retention"
	ProviderOptionPrediction           = "prediction"
	ProviderOptionMetadata             = "metadata"
	ProviderOptionModalities           = "modalities"
	ProviderOptionAudio                = "audio"
	ProviderOptionServiceTier          = "service_tier"
	ProviderOptionSafetyIdentifier     = "safety_identifier"
	ProviderOptionUser                 = "user"
	ProviderOptionVerbosity            = "verbosity"
	ProviderOptionWebSearchOptions     = "web_search_options"
	ProviderOptionParallelToolCalls    = "parallel_tool_calls"
	ProviderOptionSeed                 = "seed"
)

var providerOptionKeys = map[string]struct{}{
	ProviderOptionFrequencyPenalty:     {},
	ProviderOptionPresencePenalty:      {},
	ProviderOptionLogitBias:            {},
	ProviderOptionN:                    {},
	ProviderOptionLogprobs:             {},
	ProviderOptionTopLogprobs:          {},
	ProviderOptionStore:                {},
	ProviderOptionModeration:           {},
	ProviderOptionStreamOptions:        {},
	ProviderOptionPromptCacheKey:       {},
	ProviderOptionPromptCacheRetention: {},
	ProviderOptionPrediction:           {},
	ProviderOptionMetadata:             {},
	ProviderOptionModalities:           {},
	ProviderOptionAudio:                {},
	ProviderOptionServiceTier:          {},
	ProviderOptionSafetyIdentifier:     {},
	ProviderOptionUser:                 {},
	ProviderOptionVerbosity:            {},
	ProviderOptionWebSearchOptions:     {},
	ProviderOptionParallelToolCalls:    {},
	ProviderOptionSeed:                 {},
}

func applyProviderOptions(req *chatRequest, options map[string]any) error {
	for key := range options {
		if _, ok := providerOptionKeys[key]; !ok {
			return fmt.Errorf("openai: unsupported provider option %q", key)
		}
	}
	for key, value := range options {
		switch key {
		case ProviderOptionFrequencyPenalty:
			v, err := optionFloat64(key, value)
			if err != nil {
				return err
			}
			req.FrequencyPenalty = &v
		case ProviderOptionPresencePenalty:
			v, err := optionFloat64(key, value)
			if err != nil {
				return err
			}
			req.PresencePenalty = &v
		case ProviderOptionLogitBias:
			v, err := optionIntMap(key, value)
			if err != nil {
				return err
			}
			req.LogitBias = v
		case ProviderOptionN:
			v, err := optionInt(key, value)
			if err != nil {
				return err
			}
			req.N = &v
		case ProviderOptionLogprobs:
			v, err := optionBool(key, value)
			if err != nil {
				return err
			}
			req.Logprobs = &v
		case ProviderOptionTopLogprobs:
			v, err := optionInt(key, value)
			if err != nil {
				return err
			}
			req.TopLogprobs = &v
		case ProviderOptionStore:
			v, err := optionBool(key, value)
			if err != nil {
				return err
			}
			req.Store = &v
		case ProviderOptionModeration:
			req.Moderation = cloneAny(value)
		case ProviderOptionStreamOptions:
			v, err := optionStreamOptions(key, value)
			if err != nil {
				return err
			}
			if !req.Stream {
				return fmt.Errorf("openai: provider option %q requires stream request", key)
			}
			req.StreamOptions = v
		case ProviderOptionPromptCacheKey:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			req.PromptCacheKey = v
		case ProviderOptionPromptCacheRetention:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			if err := validatePromptCacheRetention(v); err != nil {
				return err
			}
			req.PromptCacheRetention = v
		case ProviderOptionPrediction:
			v, err := optionPrediction(key, value)
			if err != nil {
				return err
			}
			req.Prediction = v
		case ProviderOptionMetadata:
			v, err := optionStringMap(key, value)
			if err != nil {
				return err
			}
			req.Metadata = v
		case ProviderOptionModalities:
			v, err := optionStringSlice(key, value)
			if err != nil {
				return err
			}
			req.Modalities = v
		case ProviderOptionAudio:
			req.Audio = cloneAny(value)
		case ProviderOptionServiceTier:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			req.ServiceTier = v
		case ProviderOptionSafetyIdentifier:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			req.SafetyIdentifier = v
		case ProviderOptionUser:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			req.User = v
		case ProviderOptionVerbosity:
			v, err := optionString(key, value)
			if err != nil {
				return err
			}
			req.Verbosity = v
		case ProviderOptionWebSearchOptions:
			req.WebSearchOptions = cloneAny(value)
		case ProviderOptionParallelToolCalls:
			v, err := optionBool(key, value)
			if err != nil {
				return err
			}
			req.ParallelToolCalls = &v
		case ProviderOptionSeed:
			v, err := optionInt(key, value)
			if err != nil {
				return err
			}
			req.Seed = &v
		}
	}
	return nil
}

func validatePromptCacheRetention(value string) error {
	switch value {
	case "", "in_memory", "24h":
		return nil
	default:
		return fmt.Errorf("openai: prompt_cache_retention must be one of in_memory, 24h, got %q", value)
	}
}

func optionString(key string, value any) (string, error) {
	v, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("openai: provider option %q must be string", key)
	}
	return v, nil
}

func optionBool(key string, value any) (bool, error) {
	v, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("openai: provider option %q must be bool", key)
	}
	return v, nil
}

func optionFloat64(key string, value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("openai: provider option %q must be number", key)
	}
}

func optionInt(key string, value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		if v == float64(int(v)) {
			return int(v), nil
		}
	}
	return 0, fmt.Errorf("openai: provider option %q must be integer", key)
}

func optionStringMap(key string, value any) (map[string]string, error) {
	switch v := value.(type) {
	case map[string]string:
		return v, nil
	case map[string]any:
		out := make(map[string]string, len(v))
		for mapKey, mapValue := range v {
			stringValue, ok := mapValue.(string)
			if !ok {
				return nil, fmt.Errorf("openai: provider option %q values must be strings", key)
			}
			out[mapKey] = stringValue
		}
		return out, nil
	default:
		return nil, fmt.Errorf("openai: provider option %q must be map[string]string", key)
	}
}

func optionIntMap(key string, value any) (map[string]int, error) {
	switch v := value.(type) {
	case map[string]int:
		return v, nil
	case map[string]any:
		out := make(map[string]int, len(v))
		for mapKey, mapValue := range v {
			intValue, err := optionInt(key+"."+mapKey, mapValue)
			if err != nil {
				return nil, err
			}
			out[mapKey] = intValue
		}
		return out, nil
	default:
		return nil, fmt.Errorf("openai: provider option %q must be map[string]int", key)
	}
}

func optionStringSlice(key string, value any) ([]string, error) {
	switch v := value.(type) {
	case []string:
		return v, nil
	case []any:
		out := make([]string, len(v))
		for i, item := range v {
			stringItem, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("openai: provider option %q item %d must be string", key, i)
			}
			out[i] = stringItem
		}
		return out, nil
	default:
		return nil, fmt.Errorf("openai: provider option %q must be []string", key)
	}
}

func optionPrediction(key string, value any) (*prediction, error) {
	switch v := value.(type) {
	case prediction:
		return &v, nil
	case *prediction:
		return v, nil
	case map[string]any:
		typeValue, ok := v["type"].(string)
		if !ok || typeValue == "" {
			return nil, fmt.Errorf("openai: provider option %q.type is required", key)
		}
		contentValue, ok := v["content"].(string)
		if !ok {
			return nil, fmt.Errorf("openai: provider option %q.content must be string", key)
		}
		return &prediction{Type: typeValue, Content: contentValue}, nil
	default:
		return nil, fmt.Errorf("openai: provider option %q must be prediction object", key)
	}
}

func optionStreamOptions(key string, value any) (*streamOptions, error) {
	options, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("openai: provider option %q must be stream_options object", key)
	}
	out := &streamOptions{IncludeUsage: true}
	for optionKey, optionValue := range options {
		switch optionKey {
		case "include_usage":
			v, err := optionBool(key+"."+optionKey, optionValue)
			if err != nil {
				return nil, err
			}
			out.IncludeUsage = v
		case "include_obfuscation":
			v, err := optionBool(key+"."+optionKey, optionValue)
			if err != nil {
				return nil, err
			}
			out.IncludeObfuscation = &v
		default:
			return nil, fmt.Errorf("openai: unsupported stream_options field %q", optionKey)
		}
	}
	return out, nil
}

func optionResponsesStreamOptions(key string, value any) (*ResponsesStreamOptions, error) {
	options, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("openai: provider option %q must be stream_options object", key)
	}
	out := &ResponsesStreamOptions{}
	for optionKey, optionValue := range options {
		switch optionKey {
		case "include_obfuscation":
			v, err := optionBool(key+"."+optionKey, optionValue)
			if err != nil {
				return nil, err
			}
			out.IncludeObfuscation = &v
		default:
			return nil, fmt.Errorf("openai: unsupported responses stream_options field %q", optionKey)
		}
	}
	return out, nil
}
