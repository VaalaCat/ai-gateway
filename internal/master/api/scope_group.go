package api

import "github.com/VaalaCat/ai-gateway/internal/dao"

// ResolveGroupID 返回 userID 所属用户组 ID。查不到用户时返回 (0, err)。
// 放在 api 包供 token / token_template 等子包共用；只依赖 dao，不依赖 middleware。
func ResolveGroupID(q dao.AdminQuery, userID uint) (uint, error) {
	u, err := q.User().GetByID(userID)
	if err != nil {
		return 0, err
	}
	return u.GroupID, nil
}
