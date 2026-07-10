package converter

func obj(v interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func arr(v interface{}) []interface{} {
	if v == nil {
		return nil
	}
	if a, ok := v.([]interface{}); ok {
		return a
	}
	return nil
}

func str(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func int64FromAny(v interface{}) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	default:
		return 0
	}
}

func blocksFromAny(v interface{}) []map[string]interface{} {
	items := arr(v)
	blocks := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		if block := obj(item); block != nil {
			blocks = append(blocks, block)
		}
	}
	return blocks
}
