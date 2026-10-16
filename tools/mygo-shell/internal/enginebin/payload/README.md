# Engine payload

`build-native.ps1` copies `airflash-engine.exe` into this directory, and the
shell then embeds whatever is here into its own binary.

The directory is committed with only this file so a checkout without a Rust
toolchain still builds: a build with no engine here is a shell that reports it
has no engine bundled, rather than a build that fails.
