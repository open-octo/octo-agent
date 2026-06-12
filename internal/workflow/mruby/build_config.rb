# mruby build_config targeting wasm32-wasi via wasi-sdk.
# Host build produces mrbc (the bytecode compiler that runs during the build);
# the cross build produces a wasm32-wasi libmruby.a we link into a .wasm.

WASI_SDK = ENV.fetch('WASI_SDK')
SYSROOT  = "#{WASI_SDK}/share/wasi-sysroot"

# Host build: needed so the build can run mrbc on this machine.
MRuby::Build.new do |conf|
  conf.toolchain :clang
  conf.gembox 'default'
end

# Cross build: wasm32-wasi.
MRuby::CrossBuild.new('wasi') do |conf|
  conf.toolchain :clang

  conf.cc.command       = "#{WASI_SDK}/bin/clang"
  conf.linker.command   = "#{WASI_SDK}/bin/clang"
  conf.archiver.command = "#{WASI_SDK}/bin/llvm-ar"

  common = [
    "--target=wasm32-wasip1",
    "--sysroot=#{SYSROOT}",
    "-O2",
  ]
  # setjmp/longjmp on wasm requires the Exception-Handling proposal codegen.
  sjlj = ["-mllvm", "-wasm-enable-sjlj", "-mllvm", "-wasm-use-legacy-eh=false"]
  conf.cc.flags     = common + sjlj + ["-D_WASI_EMULATED_SIGNAL", "-D_WASI_EMULATED_PROCESS_CLOCKS"]
  conf.linker.flags = common + sjlj + ["-lwasi-emulated-signal", "-lwasi-emulated-process-clocks"]

  # IO-free gem set: the `stdlib` gembox (explicitly works with MRB_NO_STDIO —
  # no io/socket/dir) gives Fiber, Enumerator, Array/Hash/String ext etc., which
  # the Fiber-based scheduler needs. Plus the runtime parser + sprintf.
  conf.gem core: 'mruby-compiler'
  conf.gem core: 'mruby-error'
  conf.gem core: 'mruby-metaprog'
  conf.gem core: 'mruby-sprintf'
  conf.gem core: 'mruby-numeric-ext'
  conf.gembox 'stdlib'
end
