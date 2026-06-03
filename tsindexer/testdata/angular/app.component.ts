import { Component } from "./core";

@Component({
  selector: "app-root",
  templateUrl: "./app.component.html",
})
export class AppComponent {
  title = "hi";

  onClick(): void {
    this.log("clicked");
  }

  getName(): string {
    return this.title;
  }

  private log(m: string): void {
    console.log(m);
  }
}
