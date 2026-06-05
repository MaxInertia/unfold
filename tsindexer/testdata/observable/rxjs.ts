// Minimal RxJS-shaped stub so the fixture type-checks without the real
// package. Its path contains "rxjs", which is how the fan-out gate confirms
// the type is the real Subject (not a user class named Subject).
export class Subject<T> {
  next(_value: T): void {}
  subscribe(_observer: (value: T) => void): { unsubscribe(): void } {
    return { unsubscribe() {} };
  }
}
