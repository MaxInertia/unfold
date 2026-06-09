import { Subject } from "./rxjs";

interface Evt {
  id: number;
}

// Class-field subject — the Angular @Output / stateful-service pattern. The
// producer `this.bus.next(...)` must fan out to the `this.bus.subscribe(...)`
// callbacks, even though the subject is reached through `this`.
export class Bus {
  private bus = new Subject<Evt>();

  fire(id: number): void {
    this.bus.next({ id });
  }

  listenA(): void {
    this.bus.subscribe((e) => {
      console.log("A", e.id);
    });
  }

  listenB(): void {
    this.bus.subscribe((e) => {
      console.log("B", e.id);
    });
  }
}
