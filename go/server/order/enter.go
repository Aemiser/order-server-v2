package order

import (
	"context"
	"order_server/go/models/enum"
	"order_server/go/server/order/ai_model"
	ordertutil "order_server/go/utils/order"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var streamCtx context.Context
var streamEventName = "stream-order-data"

func OrderRec(inputText string, model enum.AIModelType, previewType enum.RecPreviewType) []ordertutil.ExceladdressInfo {
	switch model {
	case enum.AIModelType_DeepSeek:
		return ai_model.OrderRecDeepseek(inputText, previewType)
	case enum.AIModelType_Qwen:
		return ai_model.OrderRecQwen(inputText, previewType)
	}
	return nil
}

type StreamCallback func(data string) error
type DoneCallback func()

func StreamOrderRec(ctx context.Context, inputText string, model enum.AIModelType, previewType enum.RecPreviewType, callback StreamCallback, doneCallback DoneCallback) {
	streamCtx = ctx

	switch model {
	case enum.AIModelType_DeepSeek:
		ai_model.StreamRecDeepseek(inputText, previewType, callback, doneCallback)
	case enum.AIModelType_Qwen:
		ai_model.StreamRecQwen(inputText, previewType, callback, doneCallback)
	}
}

func EmitStreamData(data string) {
	if streamCtx != nil {
		runtime.EventsEmit(streamCtx, streamEventName, data)
	}
}

func EmitStreamDone() {
	if streamCtx != nil {
		runtime.EventsEmit(streamCtx, streamEventName, "DONE")
	}
}
