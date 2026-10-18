package main

import (
	"reflect"
	"testing"

	"github.com/tiaot33/mechbox/internal/library"
)

// 桌面端要覆盖技能库的全部功能：Library 的每个公开方法(Close 由服务生命周期负责)
// 都必须以同名、同签名开放给前端。
func TestLibraryServiceExposesEveryLibraryMethod(t *testing.T) {
	libType := reflect.TypeFor[*library.Library]()
	serviceType := reflect.TypeFor[*LibraryService]()
	for i := range libType.NumMethod() {
		want := libType.Method(i)
		if want.Name == "Close" {
			continue
		}
		got, ok := serviceType.MethodByName(want.Name)
		if !ok {
			t.Errorf("LibraryService lacks %s", want.Name)
			continue
		}
		// 两边的第一个入参都是接收者，从第二个开始比较。
		if got.Type.NumIn() != want.Type.NumIn() || got.Type.NumOut() != want.Type.NumOut() {
			t.Errorf("%s signature = %v; want %v", want.Name, got.Type, want.Type)
			continue
		}
		for j := 1; j < want.Type.NumIn(); j++ {
			if got.Type.In(j) != want.Type.In(j) {
				t.Errorf("%s parameter %d = %v; want %v", want.Name, j, got.Type.In(j), want.Type.In(j))
			}
		}
		for j := range want.Type.NumOut() {
			if got.Type.Out(j) != want.Type.Out(j) {
				t.Errorf("%s result %d = %v; want %v", want.Name, j, got.Type.Out(j), want.Type.Out(j))
			}
		}
	}
}
