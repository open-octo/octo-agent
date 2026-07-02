package tools

// ResolveWorkspaceDir turns the config workspace_dir value into the directory
// path a newly created web session should default its WorkingDir to.
//
//   - "" (unset) -> "" — no override, session keeps today's behavior (the
//     server's own launch directory).
func ResolveWorkspaceDir(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	return raw, nil
}
