fn main() {
    println!(
        "{}",
        serde_json::to_string(&swlsp_integration::current_catalog()).unwrap()
    );
}
