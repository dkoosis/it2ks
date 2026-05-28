package capture

type Filter struct {
	include map[string]struct{}
	exclude map[string]struct{}
}

func NewFilter(include, exclude []string) *Filter {
	f := &Filter{}
	if len(include) > 0 {
		f.include = make(map[string]struct{}, len(include))
		for _, a := range include {
			f.include[a] = struct{}{}
		}
	}
	if len(exclude) > 0 {
		f.exclude = make(map[string]struct{}, len(exclude))
		for _, a := range exclude {
			f.exclude[a] = struct{}{}
		}
	}
	return f
}

// Allow reports whether an event tagged with app should be captured.
func (f *Filter) Allow(app string) bool {
	if f.include != nil {
		if _, ok := f.include[app]; !ok {
			return false
		}
	}
	if f.exclude != nil {
		if _, ok := f.exclude[app]; ok {
			return false
		}
	}
	return true
}
