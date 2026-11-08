use super::*;

#[test]
fn phpize_cases() {
    let cases = [
        ("", PhpValue::Str(String::new())),
        ("null", PhpValue::Null),
        ("NULL", PhpValue::Null),
        ("true", PhpValue::Bool(true)),
        ("True", PhpValue::Bool(true)),
        ("false", PhpValue::Bool(false)),
        ("42", PhpValue::Int(42)),
        ("-42", PhpValue::Int(-42)),
        ("0", PhpValue::Int(0)),
        ("0700", PhpValue::Int(448)),
        ("-0700", PhpValue::Int(-448)),
        ("08", PhpValue::Str("08".to_owned())),
        ("00700", PhpValue::Str("00700".to_owned())),
        ("0b101", PhpValue::Int(5)),
        ("0x1A", PhpValue::Int(26)),
        ("1.5", PhpValue::Float(1.5)),
        ("-1.5", PhpValue::Float(-1.5)),
        ("1e3", PhpValue::Float(1000.0)),
        ("+12", PhpValue::Float(12.0)),
        ("hello", PhpValue::Str("hello".to_owned())),
        ("%kernel.debug%", PhpValue::Str("%kernel.debug%".to_owned())),
        ("yes", PhpValue::Str("yes".to_owned())),
        (
            "99999999999999999999",
            PhpValue::Str("99999999999999999999".to_owned()),
        ),
        ("1.2.3", PhpValue::Str("1.2.3".to_owned())),
    ];
    for (value, expected) in cases {
        assert_eq!(phpize(value), expected, "{value}");
    }
}
