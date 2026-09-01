package version

var (
	CheckerManager = NewCheckerManager()
	ParserManager  = NewParserManager()
)

func init() {
	must(CheckerManager.Register("mysql"))
}
