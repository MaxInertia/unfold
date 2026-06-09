import { Subject } from "./rxjs";

interface Evt {
  id: number;
}

export const events = new Subject<Evt>();

// The fan-out site: events.next(...) reaches both subscribers below.
export function emit(id: number): void {
  events.next({ id });
}

export function logger(): void {
  events.subscribe((e) => {
    console.log("log", e.id);
  });
}

export function counter(): void {
  events.subscribe((e) => {
    record(e.id);
  });
}

function record(_id: number): void {}
