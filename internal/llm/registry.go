package llm

import "fmt"

var registry = map[string]func(apiKey, model, baseURL string) Provider{}

func Register(name string, factory func(apiKey, model, baseURL string) Provider) {
	registry[name] = factory
}

func NewProvider(name, apiKey, model, baseURL string) (Provider, error) {
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	return factory(apiKey, model, baseURL), nil
}
