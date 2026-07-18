package attemptexec

import "reflect"

// ProviderExecutorAvailable reports whether provider can execute without a nil
// receiver or a missing built-in dispatcher dependency.
func ProviderExecutorAvailable(provider ProviderAttemptExecutor) bool {
	if nilDependency(provider) {
		return false
	}
	executor, ok := provider.(*Executor)
	return !ok || !nilDependency(executor.Dispatcher)
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
