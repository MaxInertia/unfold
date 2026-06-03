// Fixture for the TypeScript engine: one interface (Greeter) with two
// concrete implementations (English, French), a function that dispatches
// through the interface, and an entry point. Mirrors the Go diapp fixture.

export interface Greeter {
  greet(name: string): string;
}

export class English implements Greeter {
  greet(name: string): string {
    return `Hello, ${name}`;
  }
}

export class French implements Greeter {
  greet(name: string): string {
    return `Bonjour, ${name}`;
  }
}

export function runGreeter(g: Greeter, name: string): void {
  const msg = g.greet(name);
  console.log(msg);
}

export function main(): void {
  runGreeter(new English(), "world");
  runGreeter(new French(), "monde");
}

main();
