// seed 是一个数据填充工具，向数据库直接批量写入测试视频数据。
// 用法：cd backend && go run ./cmd/seed [--count=1000] [--author=1] [--config=configs/config.yaml]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"time"

	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/db"
	"feedsystem_video_go/internal/video"

	"gorm.io/gorm"
)

// ---------- 随机内容素材 ----------

var titles = []string{
	"夏日海滩 Vlog", "深夜做了一碗泡面", "今天去爬山啦", "周末的咖啡时光",
	"城市夜景漫步", "学了一天 Go 语言", "自制汉堡挑战", "骑行环城一圈",
	"第一次学滑板", "雨天在家打游戏", "一个人的旅行日记", "周末市集探店",
	"做了满满一桌菜", "新买的机械键盘开箱", "街头篮球练习", "深夜写代码",
	"猫咪的日常", "去看了一场演唱会", "试做日式拉面", "黄昏下的跑步",
	"读完了这本书", "买了新的植物", "手账分享", "露营第一晚",
	"和朋友打羽毛球", "晴天的公园散步", "做烘焙失败了", "第一次冲浪",
	"骑马体验记录", "夜市探吃", "换了新发型", "整理了一下书架",
}

var descriptions = []string{
	"记录生活的每一个瞬间～",
	"今天心情特别好，想和大家分享",
	"第一次尝试，效果还不错吧",
	"周末充电，感觉整个人都放松了",
	"挑战自我，坚持就是胜利！",
	"喜欢这种简单的快乐",
	"慢慢来，生活不必太着急",
	"记录下这美好的一刻",
	"和大家分享我的日常",
	"",
	"",
	"今天学到了很多新东西",
	"努力生活，认真记录",
}

func randomTitle() string        { return titles[rand.Intn(len(titles))] }
func randomDescription() string  { return descriptions[rand.Intn(len(descriptions))] }
func randomHex(n int) string {
	const chars = "abcdef0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// ---------- main ----------

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "配置文件路径")
	count   := flag.Int("count", 1000, "插入视频条数")
	authorID := flag.Uint("author", 0, "指定作者 ID（0 = 自动选第一个用户）")
	batchSize := flag.Int("batch", 200, "每批写入条数")
	flag.Parse()

	log.Printf("=== seed 开始，目标 %d 条视频 ===", *count)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	sqlDB, err := db.NewDB(cfg.Database)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer db.CloseDB(sqlDB)
	log.Printf("数据库连接成功 (%s:%d/%s)", cfg.Database.Host, cfg.Database.Port, cfg.Database.DBName)

	// 确定作者
	author := resolveAuthor(sqlDB, *authorID)
	log.Printf("使用作者: ID=%d, Username=%s", author.ID, author.Username)

	// 批量插入（Go 1.20+ 全局 rand 已自动随机种子，无需手动 Seed）
	start := time.Now()
	inserted := insertVideos(sqlDB, author, *count, *batchSize)
	elapsed := time.Since(start)

	log.Printf("=== seed 完成：成功插入 %d 条，耗时 %s ===", inserted, elapsed.Round(time.Millisecond))
}

// resolveAuthor 返回要使用的作者账号。
// 若指定了 authorID 则查找该用户；否则取数据库中第一个用户。
// 若数据库为空，则自动创建一个测试账号。
func resolveAuthor(db *gorm.DB, authorID uint) account.Account {
	var author account.Account
	if authorID > 0 {
		if err := db.First(&author, authorID).Error; err != nil {
			log.Fatalf("找不到 author_id=%d 的用户: %v", authorID, err)
		}
		return author
	}

	if err := db.First(&author).Error; err == nil {
		return author
	}

	// 没有任何用户，创建一个 seed 专用账号
	log.Printf("数据库无用户，自动创建 seed 测试账号")
	author = account.Account{
		Username: "seed_user",
		Password: "seed_pass_not_used",
	}
	if err := db.Create(&author).Error; err != nil {
		log.Fatalf("创建测试账号失败: %v", err)
	}
	log.Printf("已创建测试账号 ID=%d", author.ID)
	return author
}

// insertVideos 批量插入 total 条视频，同时写入 FeedOutbox 表，返回实际插入条数。
func insertVideos(gormDB *gorm.DB, author account.Account, total, batchSize int) int {
	ctx := context.Background()
	now := time.Now()
	inserted := 0

	videos  := make([]video.Video, 0, batchSize)
	outboxes := make([]video.FeedOutbox, 0, batchSize)

	flush := func() {
		if len(videos) == 0 {
			return
		}
		if err := gormDB.WithContext(ctx).CreateInBatches(videos, len(videos)).Error; err != nil {
			log.Fatalf("写入 videos 失败: %v", err)
		}
		// 用刚写入的视频 ID 构造 outbox 记录
		for _, v := range videos {
			outboxes = append(outboxes, video.FeedOutbox{
				AuthorID:  int64(v.AuthorID),
				PostID:    int64(v.ID),
				Score:     v.CreateTime.UnixMilli(),
				CreatedAt: v.CreateTime,
			})
		}
		if err := gormDB.WithContext(ctx).CreateInBatches(outboxes, len(outboxes)).Error; err != nil {
			log.Fatalf("写入 feed_outboxes 失败: %v", err)
		}
		inserted += len(videos)
		log.Printf("  已写入 %d / %d 条...", inserted, total)
		videos  = videos[:0]
		outboxes = outboxes[:0]
	}

	for i := range total {
		// 让 create_time 在过去 30 天内随机分布，分页测试更真实
		offset := time.Duration(rand.Int63n(int64(30 * 24 * time.Hour)))
		createTime := now.Add(-offset)

		date := createTime.Format("20060102")
		playFile  := fmt.Sprintf("/static/videos/%d/%s/%s.mp4", author.ID, date, randomHex(16))
		coverFile := fmt.Sprintf("/static/covers/%d/%s/%s.jpg", author.ID, date, randomHex(16))

		v := video.Video{
			AuthorID:    author.ID,
			Username:    author.Username,
			Title:       fmt.Sprintf("%s #%d", randomTitle(), i+1),
			Description: randomDescription(),
			PlayURL:     playFile,
			CoverURL:    coverFile,
			CreateTime:  createTime,
			LikesCount:  int64(rand.Intn(10000)),
			Popularity:  int64(rand.Intn(50000)),
		}
		videos = append(videos, v)

		if len(videos) >= batchSize {
			flush()
		}
	}
	flush() // 处理尾部不足一批的数据

	return inserted
}
