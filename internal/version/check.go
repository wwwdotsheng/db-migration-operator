package version

type Checker interface {
	Check(version, versionRange string) (bool, error)
}
