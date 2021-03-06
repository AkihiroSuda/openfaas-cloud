package llb

import (
	"fmt"

	"github.com/google/shlex"
)

type contextKeyT string

var (
	keyArgs = contextKeyT("llb.exec.args")
	keyDir  = contextKeyT("llb.exec.dir")
	keyEnv  = contextKeyT("llb.exec.env")
)

func addEnv(key, value string) StateOption {
	return addEnvf(key, value)
}
func addEnvf(key, value string, v ...interface{}) StateOption {
	return func(s State) State {
		return s.WithValue(keyEnv, getEnv(s).AddOrReplace(key, fmt.Sprintf(value, v...)))
	}
}
func clearEnv() StateOption {
	return func(s State) State {
		return s.WithValue(keyEnv, EnvList{})
	}
}
func delEnv(key string) StateOption {
	return func(s State) State {
		return s.WithValue(keyEnv, getEnv(s).Delete(key))
	}
}

func dir(str string) StateOption {
	return dirf(str)
}
func dirf(str string, v ...interface{}) StateOption {
	return func(s State) State {
		return s.WithValue(keyDir, fmt.Sprintf(str, v...))
	}
}

func reset(s_ State) StateOption {
	return func(s State) State {
		s = NewState(s.Output())
		s.ctx = s_.ctx
		return s
	}
}

func getEnv(s State) EnvList {
	v := s.Value(keyEnv)
	if v != nil {
		return v.(EnvList)
	}
	return EnvList{}
}

func getDir(s State) string {
	v := s.Value(keyDir)
	if v != nil {
		return v.(string)
	}
	return ""
}

func getArgs(s State) []string {
	v := s.Value(keyArgs)
	if v != nil {
		return v.([]string)
	}
	return nil
}

func args(args ...string) StateOption {
	return func(s State) State {
		return s.WithValue(keyArgs, args)
	}
}

func shlexf(str string, v ...interface{}) StateOption {
	return func(s State) State {
		arg, err := shlex.Split(fmt.Sprintf(str, v...))
		if err != nil {
			// TODO: handle error
		}
		return args(arg...)(s)
	}
}

type EnvList []KeyValue

type KeyValue struct {
	key   string
	value string
}

func (e EnvList) AddOrReplace(k, v string) EnvList {
	e = e.Delete(k)
	e = append(e, KeyValue{key: k, value: v})
	return e
}

func (e EnvList) Delete(k string) EnvList {
	e = append([]KeyValue(nil), e...)
	if i, ok := e.Index(k); ok {
		return append(e[:i], e[i+1:]...)
	}
	return e
}

func (e EnvList) Get(k string) (string, bool) {
	if index, ok := e.Index(k); ok {
		return e[index].value, true
	}
	return "", false
}

func (e EnvList) Index(k string) (int, bool) {
	for i, kv := range e {
		if kv.key == k {
			return i, true
		}
	}
	return -1, false
}

func (e EnvList) ToArray() []string {
	out := make([]string, 0, len(e))
	for _, kv := range e {
		out = append(out, kv.key+"="+kv.value)
	}
	return out
}
