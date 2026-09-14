/**
 * Keep hyphenated custom-element names as one editor word while excluding
 * markup punctuation and member-access separators. This follows VS Code's
 * built-in HTML word definition so Twig and HTML navigation feel consistent.
 */
export function createTwigWordPattern(): RegExp {
  return /(-?\d*\.\d\w*)|([^`~!@$^&*()=+\[{\]}\\|;:'",.<>\/\s]+)/g;
}
