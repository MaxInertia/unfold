import { Component } from "./core";

@Component({
  selector: "app-inline",
  template: `<div (click)="toggle()">{{ label() }}</div>`,
})
export class InlineComponent {
  toggle(): void {}

  label(): string {
    return "x";
  }
}
