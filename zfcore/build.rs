fn main() {
    // The app bundle loads the core from Contents/Frameworks through its
    // rpath, not from the path it was built at.
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("macos") {
        println!("cargo:rustc-cdylib-link-arg=-Wl,-install_name,@rpath/libzapfast_core.dylib");
    }
}
