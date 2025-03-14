package model

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	log "sealdice-core/utils/kratos"
)

type LogOne struct {
	// Version int           `json:"version,omitempty"`
	Items []LogOneItem `json:"items"`
	Info  LogInfo      `json:"info"`
}

type LogOneItem struct {
	ID             uint64      `json:"id" gorm:"primaryKey;autoIncrement;column:id"`
	LogID          uint64      `json:"-" gorm:"column:log_id;index:idx_log_items_log_id"`
	GroupID        string      `gorm:"index:idx_log_items_group_id;column:group_id;index:idx_log_delete_by_id"`
	Nickname       string      `json:"nickname" gorm:"column:nickname"`
	IMUserID       string      `json:"IMUserId" gorm:"column:im_userid"`
	Time           int64       `json:"time" gorm:"column:time"`
	Message        string      `json:"message"  gorm:"column:message"`
	IsDice         bool        `json:"isDice"  gorm:"column:is_dice"`
	CommandID      int64       `json:"commandId"  gorm:"column:command_id"`
	CommandInfo    interface{} `json:"commandInfo" gorm:"-"`
	CommandInfoStr string      `json:"-" gorm:"column:command_info"`
	// 这里的RawMsgID 真的什么都有可能
	RawMsgID    interface{} `json:"rawMsgId" gorm:"-"`
	RawMsgIDStr string      `json:"-" gorm:"column:raw_msg_id;index:idx_raw_msg_id;index:idx_log_delete_by_id"`
	UniformID   string      `json:"uniformId" gorm:"column:user_uniform_id"`
	// 数据库里没有的
	Channel string `json:"channel" gorm:"-"`
	// 数据库里有，JSON里没有的
	// 允许default=NULL
	Removed  *int `gorm:"column:removed" json:"-"`
	ParentID *int `gorm:"column:parent_id" json:"-"`
}

// 兼容旧版本的数据库设计
func (*LogOneItem) TableName() string {
	return "log_items"
}

// BeforeSave 钩子函数: 查询前,interface{}转换为json
func (item *LogOneItem) BeforeSave(_ *gorm.DB) (err error) {
	// 将 CommandInfo 转换为 JSON 字符串保存到 CommandInfoStr
	if item.CommandInfo != nil {
		if data, err := json.Marshal(item.CommandInfo); err == nil {
			item.CommandInfoStr = string(data)
		} else {
			return err
		}
	}

	// 将 RawMsgID 转换为 string 字符串，保存到 RawMsgIDStr
	if item.RawMsgID != nil {
		item.RawMsgIDStr = fmt.Sprintf("%v", item.RawMsgID)
	}

	return nil
}

// AfterFind 钩子函数: 查询后,interface{}转换为json
func (item *LogOneItem) AfterFind(_ *gorm.DB) (err error) {
	// 将 CommandInfoStr 从 JSON 字符串反序列化为 CommandInfo
	if item.CommandInfoStr != "" {
		if err := json.Unmarshal([]byte(item.CommandInfoStr), &item.CommandInfo); err != nil {
			return err
		}
	}

	// 将 RawMsgIDStr string 直接赋值给 RawMsgID
	if item.RawMsgIDStr != "" {
		item.RawMsgID = item.RawMsgIDStr
	}

	return nil
}

type LogInfo struct {
	ID        uint64 `json:"id" gorm:"primaryKey;autoIncrement;column:id"`
	Name      string `json:"name" gorm:"index:idx_log_group_id_name,unique"`
	GroupID   string `json:"groupId" gorm:"index:idx_logs_group;index:idx_log_group_id_name,unique"`
	CreatedAt int64  `json:"createdAt" gorm:"column:created_at"`
	UpdatedAt int64  `json:"updatedAt" gorm:"column:updated_at;index:idx_logs_update_at"`
	// 允许数据库NULL值
	// 原版代码中，此处标记了db:size，但实际上，该列并不存在。
	// 考虑到该处数据将会为未来log查询提供优化手段，保留该结构体定义，但不使用。
	// 使用GORM:<-:false 无写入权限，这样它就不会建库，但请注意，下面LogGetLogPage处，如果你查询出的名称不是size
	// 不能在这里绑定column，因为column会给你建立那一列。
	// TODO: 将这个字段使用上会不会比后台查询就JOIN更合适？
	Size *int `json:"size" gorm:"column:size"`
	// 数据库里有，json不展示的
	// 允许数据库NULL值（该字段当前不使用）
	Extra *string `json:"-" gorm:"column:extra"`
	// 原本标记为：测试版特供，由于原代码每次都会执行，故直接启用此处column记录。
	UploadURL  string `json:"-" gorm:"column:upload_url"`  // 测试版特供
	UploadTime int    `json:"-" gorm:"column:upload_time"` // 测试版特供
}

func (*LogInfo) TableName() string {
	return "logs"
}

// LogGetInfo 查询日志简略信息，使用通用函数替代SQLITE专属函数
// TODO: 换回去，因为现在已经分离了引擎
func LogGetInfo(operator DatabaseOperator) ([]int, error) {
	db := operator.GetLogDB(READ)
	lst := []int{0, 0, 0, 0}

	var maxID sql.NullInt64      // 使用sql.NullInt64来处理NULL值
	var itemsMaxID sql.NullInt64 // 使用sql.NullInt64来处理NULL值
	// 获取 logs 表的记录数和最大 ID
	err := db.Model(&LogInfo{}).Select("COUNT(*)").Scan(&lst[2]).Error
	if err != nil {
		return nil, err
	}

	err = db.Model(&LogInfo{}).Select("MAX(id)").Scan(&maxID).Error
	if err != nil {
		return nil, err
	}
	lst[0] = int(maxID.Int64)

	// 获取 log_items 表的记录数和最大 ID
	err = db.Model(&LogOneItem{}).Select("COUNT(*)").Scan(&lst[3]).Error
	if err != nil {
		return nil, err
	}

	err = db.Model(&LogOneItem{}).Select("MAX(id)").Scan(&itemsMaxID).Error
	if err != nil {
		return nil, err
	}
	lst[1] = int(itemsMaxID.Int64)

	return lst, nil
}

// Deprecated: replaced by page
func LogGetLogs(operator DatabaseOperator) ([]*LogInfo, error) {
	db := operator.GetLogDB(READ)
	var lst []*LogInfo

	// 使用 GORM 查询 logs 表
	if err := db.Model(&LogInfo{}).
		Select("id, name, group_id, created_at, updated_at").
		Find(&lst).Error; err != nil {
		return nil, err
	}

	return lst, nil
}

type QueryLogPage struct {
	PageNum          int    `db:"page_num" query:"pageNum"`
	PageSize         int    `db:"page_siz" query:"pageSize"`
	Name             string `db:"name" query:"name"`
	GroupID          string `db:"group_id" query:"groupId"`
	CreatedTimeBegin string `db:"created_time_begin" query:"createdTimeBegin"`
	CreatedTimeEnd   string `db:"created_time_end" query:"createdTimeEnd"`
}

// LogGetLogPage 获取分页
func LogGetLogPage(operator DatabaseOperator, param *QueryLogPage) (int, []*LogInfo, error) {
	db := operator.GetLogDB(READ)
	var lst []*LogInfo

	// 构建基础查询
	query := db.Model(&LogInfo{}).Select("logs.id, logs.name, logs.group_id, logs.created_at, logs.updated_at,COALESCE(logs.size, 0) as size").Order("logs.updated_at desc")
	// 添加条件
	if param.Name != "" {
		query = query.Where("logs.name LIKE ?", "%"+param.Name+"%")
	}
	if param.GroupID != "" {
		query = query.Where("logs.group_id LIKE ?", "%"+param.GroupID+"%")
	}
	if param.CreatedTimeBegin != "" {
		query = query.Where("logs.created_at >= ?", param.CreatedTimeBegin)
	}
	if param.CreatedTimeEnd != "" {
		query = query.Where("logs.created_at <= ?", param.CreatedTimeEnd)
	}

	// 获取总数
	var count int64
	if err := db.Model(&LogInfo{}).Count(&count).Error; err != nil {
		return 0, nil, err
	}

	// 分页查询
	query = query.Group("logs.id").Limit(param.PageSize).Offset((param.PageNum - 1) * param.PageSize)

	// 执行查询
	if err := query.Scan(&lst).Error; err != nil {
		return 0, nil, err
	}

	return int(count), lst, nil
}

// LogGetList 获取列表
func LogGetList(operator DatabaseOperator, groupID string) ([]string, error) {
	db := operator.GetLogDB(READ)
	var lst []string

	// 执行查询
	if err := db.Model(&LogInfo{}).
		Select("name").
		Where("group_id = ?", groupID).
		Order("updated_at DESC").
		Pluck("name", &lst).Error; err != nil {
		return nil, err
	}

	return lst, nil
}

// getIDByGroupIDAndName 获取ID 私有函数，可以直接使用db
func getIDByGroupIDAndName(db *gorm.DB, groupID string, logName string) (logID uint64, err error) {
	err = db.Model(&LogInfo{}).
		Select("id").
		Where("group_id = ? AND name = ?", groupID, logName).
		Scan(&logID).Error

	if err != nil {
		// 如果出现错误，判断是否没有找到对应的记录
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}

	return logID, nil
}

// LogGetUploadInfo 获取上传信息
func LogGetUploadInfo(operator DatabaseOperator, groupID string, logName string) (url string, uploadTime, updateTime int64, err error) {
	db := operator.GetLogDB(READ)
	var logInfo struct {
		UpdatedAt  int64  `gorm:"column:updated_at"`
		UploadURL  string `gorm:"column:upload_url"`
		UploadTime int64  `gorm:"column:upload_time"`
	}

	err = db.Model(&LogInfo{}).
		Select("updated_at, upload_url, upload_time").
		Where("group_id = ? AND name = ?", groupID, logName).
		Find(&logInfo).Error

	if err != nil {
		return "", 0, 0, err
	}

	// 提取结果
	updateTime = logInfo.UpdatedAt
	url = logInfo.UploadURL
	uploadTime = logInfo.UploadTime
	return
}

// LogSetUploadInfo 设置上传信息
func LogSetUploadInfo(operator DatabaseOperator, groupID string, logName string, url string) error {
	db := operator.GetLogDB(WRITE)
	if len(url) == 0 {
		return nil
	}

	now := time.Now().Unix()

	// 使用 GORM 更新上传信息
	err := db.Model(&LogInfo{}).Where("group_id = ? AND name = ?", groupID, logName).
		Update("upload_url", url).
		Update("upload_time", now).
		Error

	return err
}

// LogGetAllLines 获取log的所有行数据
func LogGetAllLines(operator DatabaseOperator, groupID string, logName string) ([]*LogOneItem, error) {
	db := operator.GetLogDB(READ)
	// 获取log的ID
	logID, err := getIDByGroupIDAndName(db, groupID, logName)
	if err != nil {
		return nil, err
	}

	var items []*LogOneItem

	// 查询行数据
	err = db.Model(&LogOneItem{}).
		Select("id, nickname, im_userid, time, message, is_dice, command_id, command_info, raw_msg_id, user_uniform_id").
		Where("log_id = ?", logID).
		Order("time ASC").
		Find(&items).Error

	if err != nil {
		return nil, err
	}
	return items, nil
}

type QueryLogLinePage struct {
	PageNum  int    `query:"pageNum"`
	PageSize int    `query:"pageSize"`
	GroupID  string `query:"groupId"`
	LogName  string `query:"logName"`
}

// LogGetLinePage 获取log的行分页
func LogGetLinePage(operator DatabaseOperator, param *QueryLogLinePage) ([]*LogOneItem, error) {
	db := operator.GetLogDB(READ)
	// 获取log的ID
	logID, err := getIDByGroupIDAndName(db, param.GroupID, param.LogName)
	if err != nil {
		return nil, err
	}

	var items []*LogOneItem

	// 查询行数据
	err = db.Model(&LogOneItem{}).
		Select("id, nickname, im_userid, time, message, is_dice, command_id, command_info, raw_msg_id, user_uniform_id").
		Where("log_id = ?", logID).
		Order("time ASC").
		Limit(param.PageSize).
		Offset((param.PageNum - 1) * param.PageSize).
		Scan(&items).Error

	if err != nil {
		return nil, err
	}

	return items, nil
}

// LogLinesCountGet 获取日志行数
func LogLinesCountGet(operator DatabaseOperator, groupID string, logName string) (int64, bool) {
	db := operator.GetLogDB(READ)
	// 获取日志 ID
	logID, err := getIDByGroupIDAndName(db, groupID, logName)
	if err != nil || logID == 0 {
		return 0, false
	}

	// 获取日志行数
	var count int64
	err = db.Model(&LogOneItem{}).
		Where("log_id = ? and removed IS NULL", logID).
		Count(&count).Error

	if err != nil {
		return 0, false
	}

	return count, true
}

// LogDelete 删除log
func LogDelete(operator DatabaseOperator, groupID string, logName string) bool {
	db := operator.GetLogDB(WRITE)
	// 获取 log ID
	logID, err := getIDByGroupIDAndName(db, groupID, logName)
	if err != nil || logID == 0 {
		return false
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		// 删除 log_id 相关的 log_items 记录
		if err = tx.Where("log_id = ?", logID).Delete(&LogOneItem{}).Error; err != nil {
			return err
		}

		// 删除 log_id 相关的 logs 记录
		if err = tx.Where("id = ?", logID).Delete(&LogInfo{}).Error; err != nil {
			return err
		}
		return nil
	})

	return err == nil
}

// LogAppend 向指定的log中添加一条信息
func LogAppend(operator DatabaseOperator, groupID string, logName string, logItem *LogOneItem) bool {
	db := operator.GetLogDB(WRITE)
	// 获取 log ID
	logID, err := getIDByGroupIDAndName(db, groupID, logName)
	if err != nil {
		return false
	}

	// 获取当前时间戳
	now := time.Now()
	nowTimestamp := now.Unix()
	newLogItem := LogOneItem{
		LogID:       logID,
		GroupID:     groupID,
		Nickname:    logItem.Nickname,
		IMUserID:    logItem.IMUserID,
		Time:        nowTimestamp,
		Message:     logItem.Message,
		IsDice:      logItem.IsDice,
		CommandID:   logItem.CommandID,
		CommandInfo: logItem.CommandInfo,
		RawMsgID:    logItem.RawMsgID,
		UniformID:   logItem.UniformID,
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if logID == 0 {
			// 创建一个新的 log
			newLog := LogInfo{Name: logName, GroupID: groupID, CreatedAt: nowTimestamp, UpdatedAt: nowTimestamp}
			if err = tx.Create(&newLog).Error; err != nil {
				return err
			}
			logID = newLog.ID
		}
		if err = tx.Create(&newLogItem).Error; err != nil {
			return err
		}
		// 更新 logs 表中的 updated_at 字段 和 size 字段
		if err = tx.Model(&LogInfo{}).
			Where("id = ?", logID).
			Updates(map[string]interface{}{
				"updated_at": nowTimestamp,
				"size":       gorm.Expr("COALESCE(size, 0) + ?", 1),
			}).Error; err != nil {
			return err
		}
		return nil
	})

	return err == nil
}

// LogMarkDeleteByMsgID 撤回删除
func LogMarkDeleteByMsgID(operator DatabaseOperator, groupID string, logName string, rawID interface{}) error {
	db := operator.GetLogDB(WRITE)
	// 获取 log id
	logID, err := getIDByGroupIDAndName(db, groupID, logName)
	if err != nil {
		return err
	}
	rid := fmt.Sprintf("%v", rawID)
	err = db.Transaction(func(tx *gorm.DB) error {
		if err = tx.Where("log_id = ? AND raw_msg_id = ?", logID, rid).Delete(&LogOneItem{}).Error; err != nil {
			log.Errorf("log delete error %s", err.Error())
			return err
		}
		// 更新 logs 表中的 updated_at 字段 和 size 字段
		// 真的有默认为NULL还能触发删除的情况吗？！
		if err = tx.Model(&LogInfo{}).Where("id = ?", logID).Updates(map[string]interface{}{
			"updated_at": time.Now().Unix(),
			"size":       gorm.Expr("COALESCE(size, 0) - ?", 1),
		}).Error; err != nil {
			return err
		}
		return nil
	})
	return err
}

// LogEditByMsgID 编辑日志
func LogEditByMsgID(operator DatabaseOperator, groupID, logName, newContent string, rawID interface{}) error {
	db := operator.GetLogDB(WRITE)
	logID, err := getIDByGroupIDAndName(db, groupID, logName)
	if err != nil {
		return err
	}

	rid := ""
	if rawID != nil {
		rid = fmt.Sprintf("%v", rawID)
	}

	// 更新 log_items 表中的内容
	if err := db.Model(&LogOneItem{}).
		Where("log_id = ? AND raw_msg_id = ?", logID, rid).
		Update("message", newContent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return fmt.Errorf("log edit: %w", err)
	}

	return nil
}
