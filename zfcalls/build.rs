fn main() {
    // The app bundle loads the library from Contents/Frameworks through its
    // rpath, not from the path it was built at (as zfcore/build.rs).
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("macos") {
        println!("cargo:rustc-cdylib-link-arg=-Wl,-install_name,@rpath/libzfcalls.dylib");
    }
}
