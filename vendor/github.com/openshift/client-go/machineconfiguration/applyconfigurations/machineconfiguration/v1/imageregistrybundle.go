// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// ImageRegistryBundleApplyConfiguration represents an declarative configuration of the ImageRegistryBundle type for use
// with apply.
type ImageRegistryBundleApplyConfiguration struct {
	File *string `json:"file,omitempty"`
	Data []byte  `json:"data,omitempty"`
}

// ImageRegistryBundleApplyConfiguration constructs an declarative configuration of the ImageRegistryBundle type for use with
// apply.
func ImageRegistryBundle() *ImageRegistryBundleApplyConfiguration {
	return &ImageRegistryBundleApplyConfiguration{}
}

// WithFile sets the File field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the File field is set to the value of the last call.
func (b *ImageRegistryBundleApplyConfiguration) WithFile(value string) *ImageRegistryBundleApplyConfiguration {
	b.File = &value
	return b
}

// WithData adds the given value to the Data field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Data field.
func (b *ImageRegistryBundleApplyConfiguration) WithData(values ...byte) *ImageRegistryBundleApplyConfiguration {
	for i := range values {
		b.Data = append(b.Data, values[i])
	}
	return b
}
