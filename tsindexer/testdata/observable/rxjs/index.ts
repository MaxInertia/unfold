// Minimal RxJS-shaped stub so the fixture type-checks without the real
// package. It lives in an `rxjs/` directory — just like the real package
// (node_modules/rxjs/…) — because the fan-out gate requires "rxjs" to be a
// path *segment* of the declaring file, not merely a file stem.
export class Subject<T> {
  next(_value: T): void {}
  subscribe(_observer: (value: T) => void): { unsubscribe(): void } {
    return { unsubscribe() {} };
  }
}
