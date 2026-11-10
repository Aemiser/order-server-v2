package wechatorder

// 「待人工审查」数据的内存状态与识别结果分流逻辑。
//
// 纯识别语义不变：确认订单直接进入数据预览区，不确认订单只停在这个内存列表里，
// 必须由人工点「加入预览区」才会写入 global.AddressList；任何路径都不写数据库、不生成文件。

import (
	"fmt"
	"strings"
	"time"

	"order_server/go/global"
	"order_server/go/models"
	ordertutil "order_server/go/utils/order"
)

// reviewRecord 保存审查项对应的原始订单对象，供加入预览区时直接落地，
// 避免前端展示数据与后端 global.AddressList 不一致。
type reviewRecord struct {
	Public   ReviewOrderItem
	Raw      []models.AddressInfo // 「加入预览区」要写入的订单（操作项 = 操作后的结果，删除项为空）
	Fallback []models.AddressInfo // 「忽略」时要恢复的原始订单（仅订单操作项有值，用于撤销操作）
	Source   ChatMessage          // 该审查项对应的订单消息，供操作层定位
	Promoted bool
	Removed  bool
}

// 由 autorun.go 的 runMu 统一保护：识别按钮与「加入预览区」不会同时改数据预览区。
var reviewRecords []reviewRecord

// replaceReviewRecords 仅由 runPreviewOnly（已持有 runMu）调用。
func replaceReviewRecords(records []reviewRecord) {
	reviewRecords = records
}

// ReviewOrders 返回当前仍待人工审查的数据。
func ReviewOrders() []ReviewOrderItem {
	runMu.Lock()
	defer runMu.Unlock()
	out := make([]ReviewOrderItem, 0, len(reviewRecords))
	for _, r := range reviewRecords {
		if r.Promoted || r.Removed {
			continue
		}
		out = append(out, r.Public)
	}
	return out
}

// PromoteReviewOrders 把指定审查项（ids 为空 = 全部）加入数据预览区。
// 只追加到 global.AddressList 并重新编号，返回最新的预览列表供前端刷新。
// 重复点击是幂等的：已加入的项不会再次追加。
func PromoteReviewOrders(ids []string) []ordertutil.ExceladdressInfo {
	runMu.Lock()
	defer runMu.Unlock()
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	all := len(ids) == 0
	var fresh []models.AddressInfo
	for i := range reviewRecords {
		r := &reviewRecords[i]
		if r.Promoted || r.Removed {
			continue
		}
		if !all && !want[r.Public.ID] {
			continue
		}
		if len(r.Raw) == 0 {
			r.Removed = true
			continue
		}
		fresh = append(fresh, r.Raw...)
		r.Promoted = true
	}
	if len(fresh) > 0 {
		fresh = ordertutil.SetVid(fresh) // 依据当前预览区长度续号
		global.AddressList = append(global.AddressList, fresh...)
	}
	return ordertutil.AddressToExcelAddress(global.AddressList)
}

// DiscardReviewOrders 忽略指定审查项（ids 为空 = 全部），返回剩余待审查数据。
func DiscardReviewOrders(ids []string) []ReviewOrderItem {
	runMu.Lock()
	defer runMu.Unlock()
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	all := len(ids) == 0
	var restored []models.AddressInfo
	for i := range reviewRecords {
		r := &reviewRecords[i]
		if r.Promoted || r.Removed {
			continue
		}
		if all || want[r.Public.ID] {
			r.Removed = true
			// 订单操作项被「忽略」= 撤销这次操作，把原始订单放回数据预览区。
			// 普通审查项没有 Fallback，行为保持原样（忽略即丢弃）。
			if len(r.Fallback) > 0 {
				restored = append(restored, r.Fallback...)
			}
		}
	}
	if len(restored) > 0 {
		restored = ordertutil.SetVid(restored)
		global.AddressList = append(global.AddressList, restored...)
	}
	out := make([]ReviewOrderItem, 0, len(reviewRecords))
	for _, r := range reviewRecords {
		if !r.Promoted && !r.Removed {
			out = append(out, r.Public)
		}
	}
	return out
}

// classifyResult 是一轮 AI 识别后的分流结果。
type classifyResult struct {
	Preview       []models.AddressInfo // 确认订单 → 数据预览区
	PreviewSource []ChatMessage        // 与 Preview 一一对应：每笔订单来自哪条聊天消息
	Review        []reviewRecord       // 不确认订单 → 人工审查区
	Details       []AutoOrderDetail
	AIOrders      int
	Skipped       int
	Failed        int
	DroppedByDate int
	SpecFixed     int // 商品规格被原文真值校正过的消息条数
}

// classifyItems 按「目标订单日期 + 日期标记 + AI 置信度」把识别结果分流：
//   - 有明确日期标记、且与目标日期一致、AI 未标记复核 → 确认，直接进数据预览区；
//   - 没有日期标记 / 日期与目标不符 / 置信度低 / AI 标记 needs_review → 人工审查区；
//   - 日期明确但与目标不符且开启严格模式（OnlyToday）→ 跳过，不计入任何区域；
//   - 非订单 / 请求失败 → 只出现在明细里。
//
// 没有日期标记的订单绝不会被自动补成目标日期，也不会直接进预览区——
// 这正是「通过 JSON 返回格式区分、交人工审查」的落点。
func classifyItems(items []AnalyzeItem, cfg Config, target time.Time) classifyResult {
	var out classifyResult
	out.Details = make([]AutoOrderDetail, 0, len(items))
	targetLabel := TargetDateLabel(target)

	for i, item := range items {
		msg := item.Candidate.Message
		d := AutoOrderDetail{
			Sender: msg.Sender, MessageTime: msg.Time,
			Score: item.Candidate.Score, OrderDate: item.Candidate.OrderDate,
		}
		if item.Err != nil {
			d.Error, d.Disposition = item.Err.Error(), "失败"
			out.Failed++
			out.Details = append(out.Details, d)
			continue
		}
		d.IsOrder, d.Reason = item.Result.IsOrder, item.Result.Reason
		d.OrderCount = len(item.Result.Orders)
		d.Confidence = item.Result.Confidence
		dateLabel, hasMark, matches := ResolveOrderDate(
			item.Result.OrderDate, item.Candidate.OrderDate, target)
		d.OrderDate = dateLabel
		d.IsToday = matches
		d.DateConfirmed = hasMark
		if len(item.Result.Orders) > 0 {
			d.Recipient = item.Result.Orders[0].Recipient
		}
		if !item.Result.IsOrder || len(item.Result.Orders) == 0 {
			d.Disposition = "非订单"
			out.Skipped++
			out.Details = append(out.Details, d)
			continue
		}
		out.AIOrders++
		orders := item.Result.Orders
		if FixSpecNames(msg.Content, orders) {
			out.SpecFixed++
			d.SpecFixed = true
		}

		if hasMark && !matches && cfg.OnlyToday {
			d.Disposition = "日期不符"
			d.Reason = fmt.Sprintf("订单日期 %s 与目标日期 %s 不符，严格模式下已跳过", dateLabel, targetLabel)
			out.DroppedByDate++
			out.Details = append(out.Details, d)
			continue
		}

		needsReview := !hasMark || !matches || item.Result.NeedsReview ||
			strings.EqualFold(strings.TrimSpace(item.Result.Confidence), "low")
		d.NeedsReview = needsReview
		if !needsReview {
			d.Disposition = "预览"
			out.Preview = append(out.Preview, orders...)
			for range orders {
				out.PreviewSource = append(out.PreviewSource, msg)
			}
			out.Details = append(out.Details, d)
			continue
		}

		d.Disposition = "待审查"
		why := reviewReason(hasMark, matches, dateLabel, targetLabel, item.Result)
		out.Review = append(out.Review, reviewRecord{
			Public: ReviewOrderItem{
				ID:          fmt.Sprintf("%s-%d", msg.ID, i),
				Sender:      msg.Sender,
				MessageTime: msg.Time,
				MessageDate: msg.Date,
				OrderDate:   dateLabel,
				Reason:      why,
				Confidence:  item.Result.Confidence,
				RawContent:  msg.Content,
				OrderCount:  len(orders),
				Orders:      ordertutil.AddressToExcelAddress(orders),
			},
			Raw:    orders,
			Source: msg,
		})
		out.Details = append(out.Details, d)
	}
	return out
}

func reviewReason(hasMark, matches bool, dateLabel, targetLabel string, res AIResult) string {
	switch {
	case !hasMark:
		return "消息未写日期标记（如 " + targetLabel + "），无法确认归属日期，请结合发送时间人工核对"
	case !matches:
		return fmt.Sprintf("订单日期 %s 与目标日期 %s 不符，请人工确认是否属于本轮", dateLabel, targetLabel)
	case strings.EqualFold(strings.TrimSpace(res.Confidence), "low"):
		return "AI 判定置信度偏低：" + res.Reason
	case strings.TrimSpace(res.Reason) != "":
		return "AI 建议复核：" + res.Reason
	default:
		return "字段或日期存疑，请人工核对"
	}
}
