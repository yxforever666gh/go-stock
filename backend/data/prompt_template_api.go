package data

import (
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
)

type PromptTemplateApi struct {
}

func (t PromptTemplateApi) GetPromptTemplates(name string, promptType string) *[]models.PromptTemplate {
	var result []models.PromptTemplate
	if name != "" && promptType != "" {
		db.Dao.Model(&models.PromptTemplate{}).Where("name=? and type=?", name, promptType).Find(&result)
	}
	if name != "" && promptType == "" {
		db.Dao.Model(&models.PromptTemplate{}).Where("name=?", name).Find(&result)
	}
	if name == "" && promptType != "" {
		db.Dao.Model(&models.PromptTemplate{}).Where("type=?", promptType).Find(&result)
	}
	if name == "" && promptType == "" {
		db.Dao.Model(&models.PromptTemplate{}).Find(&result)
	}

	return &result
}
func (t PromptTemplateApi) AddPrompt(template models.PromptTemplate) string {
	var tmp models.PromptTemplate
	db.Dao.Model(&models.PromptTemplate{}).Where("id=?", template.ID).First(&tmp)
	if tmp.ID == 0 {
		err := db.Dao.Model(&models.PromptTemplate{}).Create(&models.PromptTemplate{
			Content: template.Content,
			Name:    template.Name,
			Type:    template.Type,
		}).Error
		if err != nil {
			return "添加失败"
		} else {
			return "添加成功"
		}
	} else {
		err := db.Dao.Model(&models.PromptTemplate{}).Where("id=?", template.ID).Updates(template).Error
		if err != nil {
			return "更新失败"
		} else {
			return "更新成功"
		}
	}
}

func (t PromptTemplateApi) DelPrompt(Id uint) string {
	template := &models.PromptTemplate{}
	db.Dao.Model(template).Where("id=?", Id).Find(template)
	if template.ID > 0 {
		err := db.Dao.Model(template).Delete(template).Error
		if err != nil {
			return "删除失败"
		} else {
			return "删除成功"
		}
	}
	return "模板信息不存在"
}

func (t PromptTemplateApi) GetPromptTemplateByID(id int) string {
	prompt := &models.PromptTemplate{}
	db.Dao.Model(&models.PromptTemplate{}).Where("id=?", id).First(prompt)
	logger.SugaredLogger.Infof("GetPromptTemplateByID:%d %s", id, prompt.Content)
	return prompt.Content
}
func NewPromptTemplateApi() *PromptTemplateApi {
	return &PromptTemplateApi{}
}
