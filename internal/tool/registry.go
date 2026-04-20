package tool

var registry = map[string]Tool{}

func Register(t Tool) {
	registry[t.Name()] = t
}

func All() map[string]Tool {
	return registry
}

func Get(name string) (Tool, bool) {
	t, ok := registry[name]
	return t, ok
}
