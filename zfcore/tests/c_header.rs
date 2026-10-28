//! zfcore.h compiles as strict C, and a C program driving the demo core
//! through it reaches the chat list (tests/c/measure.c, also the
//! measurement tool).

#![cfg(target_os = "macos")]

use std::path::Path;
use std::process::Command;

#[test]
fn a_c_program_on_the_header_reaches_the_chat_list() {
    let crate_dir = Path::new(env!("CARGO_MANIFEST_DIR"));
    let exe = Path::new(env!("CARGO_TARGET_TMPDIR")).join("zf-measure");
    let status = Command::new("cc")
        .args(["-std=c11", "-Wall", "-Wextra", "-Werror", "-I"])
        .arg(crate_dir.join("include"))
        .arg(crate_dir.join("tests/c/measure.c"))
        .arg("-o")
        .arg(&exe)
        .status()
        .expect("a C compiler");
    assert!(
        status.success(),
        "measure.c did not compile against zfcore.h"
    );
    let dylib = std::env::current_exe()
        .unwrap()
        .parent()
        .unwrap()
        .join("libzapfast_core.dylib");
    let output = Command::new(&exe).arg(&dylib).arg("2").output().unwrap();
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(
        output.status.success(),
        "{stdout}{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(stdout.contains("last chat-list chunk"), "{stdout}");
    eprintln!("{stdout}");
}
