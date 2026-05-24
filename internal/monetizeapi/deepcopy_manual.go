package monetizeapi

// PreSignedAuth deep-copy is hand-written because its Payment field is
// an opaque map[string]interface{} (controller-gen can't deep-copy
// untyped JSON). The type is excluded from generation via the
// object-generate=false marker in types.go.

// DeepCopyInto copies the receiver into out. The Payment map is
// shallow-copied; values inside the map are JSON-serializable scalars /
// maps / slices passed through to the buyer sidecar, so a shallow copy
// is sufficient for the controller's deep-copy contract (no internal
// pointer aliasing into caller-owned mutable structures).
func (in *PreSignedAuth) DeepCopyInto(out *PreSignedAuth) {
	*out = *in
	if in.Payment != nil {
		out.Payment = deepCopyJSONMap(in.Payment)
	}
}

// DeepCopy returns a deep copy of the receiver.
func (in *PreSignedAuth) DeepCopy() *PreSignedAuth {
	if in == nil {
		return nil
	}
	out := new(PreSignedAuth)
	in.DeepCopyInto(out)
	return out
}

// deepCopyJSONMap walks an opaque JSON-decoded map[string]interface{}
// tree and returns a structurally identical copy. Handles the nested
// shapes the x402 PaymentPayload uses (object, array, scalar).
func deepCopyJSONMap(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = deepCopyJSONValue(v)
	}
	return out
}

func deepCopyJSONValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return deepCopyJSONMap(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, item := range t {
			out[i] = deepCopyJSONValue(item)
		}
		return out
	default:
		// Strings, numbers, bools, nil — value types, safe to share.
		return v
	}
}
