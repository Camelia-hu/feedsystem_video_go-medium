package feed

import "encoding/json"

// BucketDetail 记录某种 feed 类型的游标状态
// 服务端每次返回更新后的值，客户端原样带回下一次请求
type BucketDetail struct {
	Type        QueryType `json:"type"`
	ScoreBefore      int64 `json:"score_before,omitempty"`       // latest: unix时间戳; likes: likes_count; following: inbox score; popularity DB: popularity值
	OutboxScoreBefore int64 `json:"outbox_score_before,omitempty"` // followings: outbox 游标，与 ScoreBefore(inbox) 独立推进
	IDBefore         uint  `json:"id_before,omitempty"`           // likes/popularity DB: id 二级游标
	Offset           int   `json:"offset,omitempty"`              // popularity Redis: ZREVRANGE 起始偏移
	AsOf             int64 `json:"as_of,omitempty"`               // popularity Redis: 快照分钟时间戳
	TimeBefore       int64 `json:"time_before,omitempty"`         // popularity DB fallback: create_time 游标
}

// BucketCollection 一次请求中各数据源的游标集合
type BucketCollection map[QueryType]*BucketDetail

func bucketFromString(s string) (BucketCollection, error) {
	var bc BucketCollection
	return bc, json.Unmarshal([]byte(s), &bc)
}

func (bc BucketCollection) toString() (string, error) {
	b, err := json.Marshal(bc)
	return string(b), err
}
