package settings

import (
	"context"
	"testing"
)

type memoryConfigRepo struct {
	values map[string]string
}

func (r *memoryConfigRepo) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := r.values[key]
	return v, ok, nil
}

func (r *memoryConfigRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *memoryConfigRepo) All(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func TestStringAllowEmptyDistinguishesUnsetAndExplicitEmpty(t *testing.T) {
	repo := &memoryConfigRepo{values: map[string]string{}}
	svc, err := New(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.StringAllowEmpty(KeyQuarkTVProxyClients); got != "vidhub" {
		t.Fatalf("未设置时=%q，期望默认值 vidhub", got)
	}
	if err := svc.Update(context.Background(), map[string]string{KeyQuarkTVProxyClients: ""}); err != nil {
		t.Fatal(err)
	}
	if got := svc.StringAllowEmpty(KeyQuarkTVProxyClients); got != "" {
		t.Fatalf("显式清空后=%q，期望保留空值", got)
	}
	if got := svc.String(KeyQuarkTVProxyClients); got != "vidhub" {
		t.Fatalf("原 String 语义不应改变，实际=%q", got)
	}
	if got := svc.StringAllowEmpty(KeyFnosDirectSTRMClients); got != "Infuse" {
		t.Fatalf("飞牛直读客户端未设置时=%q，期望默认值 Infuse", got)
	}
	if err := svc.Update(context.Background(), map[string]string{KeyFnosDirectSTRMClients: ""}); err != nil {
		t.Fatal(err)
	}
	if got := svc.StringAllowEmpty(KeyFnosDirectSTRMClients); got != "" {
		t.Fatalf("飞牛直读客户端显式清空后=%q，期望保留空值", got)
	}
}
