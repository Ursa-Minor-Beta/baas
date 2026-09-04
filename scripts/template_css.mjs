import { fs } from "zx";
import { program } from "commander";
import postcss from "postcss";
import postcssCustomProperties from "postcss-custom-properties";

async function main() {
  program
    .argument("<input>", "input CSS file path")
    .argument("<output>", "output CSS file path")
    .option("-t, --template <template>", "template JSON")
    .parse();

  const [inputPath, outputPath] = program.args;
  const opts = program.opts();
  const template = opts.template == null ? {} : JSON.parse(opts.template);

  const fontSize = template.fontSize ?? 12;

  const inputCssTemplate = await fs.readFile(inputPath, "utf-8");
  const inputCss = inputCssTemplate
    .replace("{primaryColor}", template.primaryColor ?? "black")
    .replace("{secondaryColor}", template.secondaryColor ?? "black")
    .replace("{accentColor}", template.accentColor ?? "black")
    .replace("{textColor}", template.textColor ?? "black")
    .replace("{fontFamily}", template.fontFamily ?? "Arial, sans-serif")
    .replace("{headingFont}", template.headingFont ?? "Arial, sans-serif")
    .replace("{fontSize}", fontSize)
    .replace("{h1FontSize}", template.h1FontSize ?? fontSize * 2)
    .replace("{h2FontSize}", template.h2FontSize ?? fontSize * 1.6)
    .replace("{h3FontSize}", template.h3FontSize ?? fontSize * 1.3)
    .replace("{codeFontSize}", template.codeFontSize ?? fontSize - 2);
  const outputCss = await postcss([postcssCustomProperties({ preserve: false })]).process(inputCss, {
    from: inputPath,
  });
  await fs.writeFile(outputPath, outputCss.css);
}
main();
