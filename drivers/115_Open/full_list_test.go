package pan115open

import (
	"context"
	"reflect"
	"testing"
)

func TestCollectFullListPagesContinuesAfterShortPageAndLowCount(t *testing.T) {
	offsets := make([]int, 0, 3)
	pages := []listPageResp{
		{Count: 1, Data: []fileEntry{{Fid: "f1", Fn: "1.mkv", Pid: "d1"}, {Fid: "f2", Fn: "2.mkv", Pid: "d1"}}},
		{Count: 2, Data: []fileEntry{{Fid: "f3", Fn: "3.mkv", Pid: "d2"}}},
		{},
	}
	entries, err := collectFullListPages(context.Background(), func(_ context.Context, offset, _ int) (listPageResp, error) {
		offsets = append(offsets, offset)
		page := pages[0]
		pages = pages[1:]
		return page, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("完整清单条目数 = %d，期望 3", len(entries))
	}
	if !reflect.DeepEqual(offsets, []int{0, 2, 3}) {
		t.Fatalf("分页 offset = %v，期望 [0 2 3]", offsets)
	}
}

func TestCollectFullListPagesRejectsRepeatedPage(t *testing.T) {
	page := listPageResp{Count: 99, Data: []fileEntry{{Fid: "f1", Fn: "1.mkv", Pid: "d1"}}}
	calls := 0
	_, err := collectFullListPages(context.Background(), func(_ context.Context, _, _ int) (listPageResp, error) {
		calls++
		return page, nil
	})
	if err == nil {
		t.Fatal("重复分页必须中止，不能把不完整清单交给上层")
	}
	if calls != 2 {
		t.Fatalf("重复分页调用次数 = %d，期望 2", calls)
	}
}

func TestCollectFullListPagesDeduplicatesOverlappingEntries(t *testing.T) {
	pages := []listPageResp{
		{Data: []fileEntry{{Fid: "f1", Fn: "1.mkv", Pid: "d1"}, {Fid: "f2", Fn: "2.mkv", Pid: "d1"}}},
		{Data: []fileEntry{{Fid: "f2", Fn: "2.mkv", Pid: "d1"}, {Fid: "f3", Fn: "3.mkv", Pid: "d2"}}},
		{},
	}
	entries, err := collectFullListPages(context.Background(), func(_ context.Context, _, _ int) (listPageResp, error) {
		page := pages[0]
		pages = pages[1:]
		return page, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("重叠分页去重后条目数 = %d，期望 3", len(entries))
	}
}
