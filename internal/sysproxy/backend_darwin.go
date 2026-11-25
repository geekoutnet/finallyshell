package sysproxy

func NativeBackend() Backend { return macBackend{runCommand} }
