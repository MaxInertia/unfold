// Offset-convention fixture. Call spans are emitted as UTF-16 string
// indices to match how the frontend reads them, so a call preceded by
// multibyte text (emoji are surrogate pairs; accented letters are
// multibyte in UTF-8) must still slice to the function name.

function greetUnicode(name: string): string {
  return `Hej, ${name}`;
}

export function wave(): void {
  const flag = "🇸🇪 café — 👋🌍"; // multibyte text before the call below
  console.log(greetUnicode(flag));
}
