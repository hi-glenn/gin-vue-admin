package system

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/flipped-aurora/gin-vue-admin/server/utils/ast"
	"github.com/pkg/errors"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flipped-aurora/gin-vue-admin/server/global"
	common "github.com/flipped-aurora/gin-vue-admin/server/model/common/request"
	model "github.com/flipped-aurora/gin-vue-admin/server/model/system"
	request "github.com/flipped-aurora/gin-vue-admin/server/model/system/request"
	"github.com/flipped-aurora/gin-vue-admin/server/utils"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

var AutocodeHistory = new(autoCodeHistory)

type autoCodeHistory struct{}

// Create 创建代码生成器历史记录
// Author [SliverHorn](https://github.com/SliverHorn)
// Author [songzhibin97](https://github.com/songzhibin97)
func (s *autoCodeHistory) Create(ctx context.Context, info request.SysAutoHistoryCreate) error {
	create := info.Create()
	err := global.GVA_DB.WithContext(ctx).Create(&create).Error
	if err != nil {
		return errors.Wrap(err, "创建失败!")
	}
	return nil
}

// First 根据id获取代码生成器历史的数据
// Author [SliverHorn](https://github.com/SliverHorn)
// Author [songzhibin97](https://github.com/songzhibin97)
func (s *autoCodeHistory) First(ctx context.Context, info common.GetById) (string, error) {
	var meta string
	err := global.GVA_DB.WithContext(ctx).Model(model.SysAutoCodeHistory{}).Where("id = ?", info.ID).Pluck("request", &meta).Error
	if err != nil {
		return "", errors.Wrap(err, "获取失败!")
	}
	return meta, nil
}

// Repeat 检测重复
// Author [SliverHorn](https://github.com/SliverHorn)
// Author [songzhibin97](https://github.com/songzhibin97)
func (s *autoCodeHistory) Repeat(businessDB, structName, Package string) bool {
	var count int64
	global.GVA_DB.Model(&model.SysAutoCodeHistory{}).Where("business_db = ? and struct_name = ? and package = ? and flag = 0", businessDB, structName, Package).Count(&count)
	return count > 0
}

// RollBack 回滚
// Author [SliverHorn](https://github.com/SliverHorn)
// Author [songzhibin97](https://github.com/songzhibin97)
func (s *autoCodeHistory) RollBack(ctx context.Context, info request.SysAutoHistoryRollBack) error {
	var history model.SysAutoCodeHistory
	err := global.GVA_DB.Where("id = ?", info.ID).First(&history).Error
	if err != nil {
		return err
	}
	if history.ExportTemplateID != 0 {
		err = global.GVA_DB.Delete(&model.SysExportTemplate{}, "id = ?", history.ExportTemplateID).Error
		if err != nil {
			return err
		}
	}
	if info.DeleteApi {
		ids := info.ApiIds(history)
		err = ApiServiceApp.DeleteApisByIds(ids)
		if err != nil {
			global.GVA_LOG.Error("ClearTag DeleteApiByIds:", zap.Error(err))
		}
	} // 清除API表
	if info.DeleteMenu {
		err = BaseMenuServiceApp.DeleteBaseMenu(int(history.MenuID))
		if err != nil {
			return errors.Wrap(err, "删除菜单失败!")
		}
	} // 清除菜单表
	if info.DeleteTable {
		err = s.DropTable(history.BusinessDB, history.Table)
		if err != nil {
			return errors.Wrap(err, "删除表失败!")
		}
	} // 删除表
	templates := make(map[string]string, len(history.Templates))
	for key, template := range history.Templates {
		{
			server := filepath.Join(global.GVA_CONFIG.AutoCode.Root, global.GVA_CONFIG.AutoCode.Server)
			keys := strings.Split(key, "/")
			key = filepath.Join(keys...)
			key = strings.TrimPrefix(key, server)
		} // key
		{
			web := filepath.Join(global.GVA_CONFIG.AutoCode.Root, global.GVA_CONFIG.AutoCode.WebRoot())
			server := filepath.Join(global.GVA_CONFIG.AutoCode.Root, global.GVA_CONFIG.AutoCode.Server)
			slices := strings.Split(template, "/")
			template = filepath.Join(slices...)
			ext := path.Ext(template)
			switch ext {
			case ".js", ".vue":
				template = filepath.Join(web, template)
			case ".go":
				template = filepath.Join(server, template)
			}
		} // value
		templates[key] = template
	}
	history.Templates = templates
	for key, value := range history.Injections {
		var injection ast.Ast
		switch key {
		case ast.TypePackageApiEnter, ast.TypePackageRouterEnter, ast.TypePackageServiceEnter:

		case ast.TypePackageApiModuleEnter, ast.TypePackageRouterModuleEnter, ast.TypePackageServiceModuleEnter:
			var entity ast.PackageModuleEnter
			_ = json.Unmarshal([]byte(value), &entity)
			injection = &entity
		case ast.TypePackageInitializeGorm:
			var entity ast.PackageInitializeGorm
			_ = json.Unmarshal([]byte(value), &entity)
			injection = &entity
		case ast.TypePackageInitializeRouter:
			var entity ast.PackageInitializeRouter
			_ = json.Unmarshal([]byte(value), &entity)
			injection = &entity
		case ast.TypePluginGen:
			var entity ast.PluginGen
			_ = json.Unmarshal([]byte(value), &entity)
			injection = &entity
		case ast.TypePluginApiEnter, ast.TypePluginRouterEnter, ast.TypePluginServiceEnter:
			var entity ast.PluginEnter
			_ = json.Unmarshal([]byte(value), &entity)
			injection = &entity
		case ast.TypePluginInitializeGorm:
			var entity ast.PluginInitializeGorm
			_ = json.Unmarshal([]byte(value), &entity)
			injection = &entity
		case ast.TypePluginInitializeRouter:
			var entity ast.PluginInitializeRouter
			_ = json.Unmarshal([]byte(value), &entity)
			injection = &entity
		}
		if injection == nil {
			continue
		}
		file, _ := injection.Parse("", nil)
		if file != nil {
			_ = injection.Rollback(file)
			err = injection.Format("", nil, file)
			if err != nil {
				return err
			}
			fmt.Printf("[filepath:%s]回滚注入代码成功!\n", key)
		}
	} // 清除注入代码
	removeBasePath := filepath.Join(global.GVA_CONFIG.AutoCode.Root, "rm_file", strconv.FormatInt(int64(time.Now().Nanosecond()), 10))
	for _, value := range history.Templates {
		if !filepath.IsAbs(value) {
			continue
		}
		removePath := filepath.Join(removeBasePath, strings.TrimPrefix(value, global.GVA_CONFIG.AutoCode.Root))
		err = utils.FileMove(value, removePath)
		if err != nil {
			return errors.Wrapf(err, "[src:%s][dst:%s]文件移动失败!", value, removePath)
		}
	} // 移动文件

	err = global.GVA_DB.WithContext(ctx).Model(&model.SysAutoCodeHistory{}).Where("id = ?", info.ID).Update("flag", 1).Error
	if err != nil {
		return errors.Wrap(err, "更新失败!")
	}

	localPath := path.Join(global.GVA_CONFIG.AutoCode.Root, global.GVA_CONFIG.AutoCode.Server, "resource", "lang")
	files, err := s.readJSONFiles(localPath)
	if err != nil {
		return err
	}
	if info.DeleteMenu {
		for _, file := range files {
			err := s.reWriteI18nJson(file, "menu", history.Package, history.StructName)
			if err != nil {
				return err
			}
		}
	}

	if info.DeleteApi {
		for _, file := range files {
			err := s.reWriteI18nJson(file, "api", history.Package, history.StructName)
			if err != nil {
				return err
			}
		}
	}

	localPath = path.Join(global.GVA_CONFIG.AutoCode.Root, global.GVA_CONFIG.AutoCode.Web, "locales")
	files, err = s.readJSONFiles(localPath)
	if err != nil {
		return err
	}
	for _, file := range files {
		err := s.reWriteI18nJson(file, "web", history.Package, history.StructName)
		if err != nil {
			return err
		}
	}
	return nil
}

// Delete 删除历史数据
// Author [SliverHorn](https://github.com/SliverHorn)
// Author [songzhibin97](https://github.com/songzhibin97)
func (s *autoCodeHistory) Delete(ctx context.Context, info common.GetById) error {
	err := global.GVA_DB.WithContext(ctx).Where("id = ?", info.Uint()).Delete(&model.SysAutoCodeHistory{}).Error
	if err != nil {
		return errors.Wrap(err, "删除失败!")
	}
	return nil
}

// GetList 获取系统历史数据
// Author [SliverHorn](https://github.com/SliverHorn)
// Author [songzhibin97](https://github.com/songzhibin97)
func (s *autoCodeHistory) GetList(ctx context.Context, info common.PageInfo) (list []model.SysAutoCodeHistory, total int64, err error) {
	var entities []model.SysAutoCodeHistory
	db := global.GVA_DB.WithContext(ctx).Model(&model.SysAutoCodeHistory{})
	err = db.Count(&total).Error
	if err != nil {
		return nil, total, err
	}
	err = db.Scopes(info.Paginate()).Order("updated_at desc").Find(&entities).Error
	return entities, total, err
}

// DropTable 获取指定数据库和指定数据表的所有字段名,类型值等
// @author: [piexlmax](https://github.com/piexlmax)
func (s *autoCodeHistory) DropTable(BusinessDb, tableName string) error {
	if BusinessDb != "" {
		return global.MustGetGlobalDBByDBName(BusinessDb).Exec("DROP TABLE " + tableName).Error
	} else {
		return global.GVA_DB.Exec("DROP TABLE " + tableName).Error
	}
}

func (s *autoCodeHistory) readJSONFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".json" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (s *autoCodeHistory) reWriteI18nJson(file string, flag string, packageName, structName string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	originJson := string(data)
	switch flag {
	case "api":
		var err error
		originJson, err = sjson.Delete(originJson, fmt.Sprintf("system.api.group.%s%s", packageName, structName))
		if err != nil {
			return err
		}
		originJson, err = sjson.Delete(originJson, fmt.Sprintf("system.api.desc.add%s%s", packageName, structName))
		if err != nil {
			return err
		}
		originJson, err = sjson.Delete(originJson, fmt.Sprintf("system.api.desc.delete%s%s", packageName, structName))
		if err != nil {
			return err
		}
		originJson, err = sjson.Delete(originJson, fmt.Sprintf("system.api.desc.batch%s%s", packageName, structName))
		if err != nil {
			return err
		}
		originJson, err = sjson.Delete(originJson, fmt.Sprintf("system.api.desc.update%s%s", packageName, structName))
		if err != nil {
			return err
		}
		originJson, err = sjson.Delete(originJson, fmt.Sprintf("system.api.desc.find%s%s", packageName, structName))
		if err != nil {
			return err
		}
		originJson, err = sjson.Delete(originJson, fmt.Sprintf("system.api.desc.list%s%s", packageName, structName))
		if err != nil {
			return err
		}
	case "menu":
		originJson, err = sjson.Delete(originJson, fmt.Sprintf("system.menu.%s%s", packageName, structName))
		if err != nil {
			return err
		}
	case "web":
		originJson, err = sjson.Delete(originJson, fmt.Sprintf("%s.%s", packageName, structName))
		if err != nil {
			return err
		}
	}
	err = os.WriteFile(file, []byte(originJson), 0666)
	if err != nil {
		return err
	}
	return nil
}
