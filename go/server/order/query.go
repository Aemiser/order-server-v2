package order

import (
	"fmt"
	"order_server/go/common"
	"order_server/go/global"
	"order_server/go/models"
	ordertutil "order_server/go/utils/order"
	"time"

	"github.com/sirupsen/logrus"
)

type OrderQueryRequest struct {
	common.PageInfo
	Addresssinfo models.AddressInfo
	Starttime    time.Time `json:"startTime"`
	Endtime      time.Time `json:"endTime"`
}

type OrderQueryResponse struct {
	List  []ordertutil.ExceladdressInfo `json:"list"`
	Count int                           `json:"count"`
}

func OrderQuery(oq OrderQueryRequest) (resp OrderQueryResponse) {

	var addresslist = oq.Addresssinfo
	fmt.Println("参数列表：", addresslist)
	fmt.Println("参数列表page：", oq.Page)
	fmt.Println("参数列表limit：", oq.Limit)
	var option = common.Options{
		PageInfo:     oq.PageInfo,
		DefaultOrder: "created_at",
		Likes:        []string{"recipient", "address", "phone", "product_specs", "remarks"},
		Debug:        true,
		Where:        nil,
	}
	// 判断时间范围
	if !(oq.Starttime.IsZero() && oq.Endtime.IsZero()) {
		// 初始化 Where 字段
		option.Where = global.Db.Where("")
		option.Where.Where("created_at between ? and ?", oq.Starttime, oq.Endtime)

	}

	_list, count, err := common.ListQuery(addresslist, option)
	if err != nil {
		logrus.Errorf("查询失败 %s", err)
		return
	}

	list := ordertutil.AddressToExcelAddress(_list)
	resp = OrderQueryResponse{
		List:  list,
		Count: count,
	}
	for _, info := range list {
		fmt.Println(info)
	}
	return
}
