package version

import "context"

type MysqlParser struct {
}

var _ = (*MysqlParser)(nil)

func NewMysqlParser() *MysqlParser {
	return &MysqlParser{}
}

func (parser *MysqlParser) GetVersion(ctx context.Context) (string, error) {
	return "", nil
}
