import { $, fs, tempfile } from "zx";
import { InvalidOptionArgumentError, Option, program } from "commander";
import * as pdf2pic from "pdf2pic";
import { Poppler } from "node-poppler";

let DEBUG = false;

async function convertPdf2Pic(inputPath, { pageNumbers, density, width, height }) {
  const optimalDimensions = getOptimalDimensions(density);
  const pages = await pdf2pic
    .fromPath(inputPath, {
      density,
      quality: 100,
      format: "png",
      width: width ?? optimalDimensions.width,
      height: height ?? optimalDimensions.height,
    })
    .bulk(pageNumbers.map(inc), { responseType: "buffer" });
  return pages.map((page) => page.buffer);
}

function getOptimalDimensions(density) {
  // A4 dimensions in inches
  const A4_WIDTH_INCHES = 8.27;
  const A4_HEIGHT_INCHES = 11.69;

  // Calculate pixels based on DPI
  const width = Math.round(A4_WIDTH_INCHES * density);
  const height = Math.round(A4_HEIGHT_INCHES * density);

  return { width, height };
}

async function splitDefault(inputPath, pageNumbers) {
  return { inputPath, pageNumbers };
}

async function splitQpdf(inputPath, pageNumbers) {
  const pdfBatchPart = tempfile();
  try {
    await $({ quiet: !DEBUG })`qpdf ${inputPath} --pages . ${pageNumbers.map(inc).join(",")} -- ${pdfBatchPart}`;
    const batchPageNumbers = pageNumbers.map((_, index) => index);
    return { inputPath: pdfBatchPart, pageNumbers: batchPageNumbers, cleanup: async () => fs.remove(pdfBatchPart) };
  } catch (e) {
    await fs.remove(pdfBatchPart);
    throw e;
  }
}

async function splitMutool(inputPath, pageNumbers) {
  const pdfBatchPath = tempfile();
  try {
    await $({ quiet: !DEBUG })`mutool merge -o ${pdfBatchPath} ${inputPath} ${pageNumbers.map(inc).join(",")}`;
    const newPageNumbers = pageNumbers.map((_, index) => index);
    return { inputPath: pdfBatchPath, pageNumbers: newPageNumbers, cleanup: async () => fs.remove(pdfBatchPath) };
  } catch (e) {
    await fs.remove(pdfBatchPath);
    throw e;
  }
}

async function countPagesPoppler(inputPath) {
  const poppler = new Poppler();
  const info = await poppler.pdfInfo(inputPath, { printAsJson: true });
  return parseInt(info.pages, 10);
}

async function countPagesQpdf(inputPath) {
  const result = await $({ quiet: !DEBUG })`qpdf --show-npages ${inputPath}`;
  return parseInt(result.stdout.trim(), 10);
}

const conversionMethods = {
  pdf2pic: convertPdf2Pic,
};

const splittingMethods = {
  default: splitDefault,
  qpdf: splitQpdf,
  mutool: splitMutool,
};

const pageCountingMethods = {
  poppler: countPagesPoppler,
  qpdf: countPagesQpdf,
};

async function main() {
  program
    .addOption(
      new Option("-c, --conversionMethod <method>", "conversion method")
        .choices(Object.keys(conversionMethods))
        .default("pdf2pic"),
    )
    .addOption(
      new Option("-p, --pageCountingMethod <method>", "page counting method")
        .choices(Object.keys(pageCountingMethods))
        .default("qpdf"),
    )
    .addOption(
      new Option("-s, --splittingMethod <method>", "splitting method")
        .choices(Object.keys(splittingMethods))
        .default("default"),
    )
    .addOption(
      new Option("--fallbackSplitting [allowed]", "allow fallback to default splitting method")
        .default(true)
        .argParser((value) => value.trim() === "true" || value.trim() === "0"),
    )
    .addOption(
      new Option("-b, --batchSize <number>", "batch size, 0 for all pages at once")
        .default(10)
        .argParser(parseIntOption),
    )
    .addOption(new Option("-d, --density <number>", "image density, DPI").default(100).argParser(parseIntOption))
    .addOption(
      new Option("-w, --width <number>", "image width in pixels, don't set to determine automatically")
        .default(null)
        .argParser(parseIntOption),
    )
    .addOption(
      new Option("-h, --height <number>", "image height in pixels, don't set to determine automatically")
        .default(null)
        .argParser(parseIntOption),
    )
    .addOption(new Option("-o, --output <path>", "output directory path, defaults to temp directory").default(null))
    .addOption(new Option("-D, --debug", "enable debug mode").default(false))
    .argument("<input>", "path to the PDF file")
    .argument("<output>", "path to the output JSON file");

  program.parse();

  const opts = program.opts();
  const { conversionMethod, splittingMethod, pageCountingMethod, batchSize, density, width, height } = opts;
  const inputPath = program.args[0];
  const outputPath = program.args[1];

  DEBUG = opts.debug;

  console.log("Input path:", inputPath);
  console.log("Output path:", outputPath);
  console.log("Options:", opts);

  const pageCount = await pageCountingMethods[pageCountingMethod](inputPath);
  console.log("Total pages in PDF:", pageCount);

  const results = [];
  for (let batchStart = 0; batchStart < pageCount; batchStart += batchSize === 0 ? pageCount : batchSize) {
    const pageNumbers = [];
    for (
      let pageNumber = batchStart;
      pageNumber < Math.min(batchStart + (batchSize === 0 ? pageCount : batchSize), pageCount);
      pageNumber++
    ) {
      pageNumbers.push(pageNumber);
    }
    console.log("Processing pages:", pageNumbers);

    let batchInputPath, batchPageNumbers, cleanup;
    try {
      const splittingResult = await splittingMethods[splittingMethod](inputPath, pageNumbers);
      batchInputPath = splittingResult.inputPath;
      batchPageNumbers = splittingResult.pageNumbers;
      cleanup = splittingResult.cleanup;
    } catch (e) {
      if (opts.fallbackSplitting && splittingMethod !== "default") {
        console.log(`Splitting with method "${splittingMethod}" failed, falling back to "default" method. Error:`, e);
        const splittingResult = await splittingMethods["default"](inputPath, pageNumbers);
        batchInputPath = splittingResult.inputPath;
        batchPageNumbers = splittingResult.pageNumbers;
        cleanup = splittingResult.cleanup;
      } else {
        throw e;
      }
    }
    try {
      const imageBuffers = await conversionMethods[conversionMethod](batchInputPath, {
        pageNumbers: batchPageNumbers,
        density,
        width,
        height,
      });
      for (const imageBuffer of imageBuffers) {
        let outputPath;
        if (opts.output) {
          outputPath = `${opts.output}/output_page_${results.length + 1}.png`;
        } else {
          outputPath = tempfile("output.png");
        }
        await fs.writeFile(outputPath, imageBuffer);
        results.push(outputPath);
        console.log("Output file created:", outputPath);
      }
    } finally {
      await cleanup?.();
    }
  }

  await fs.writeFile(outputPath, JSON.stringify(results));
}

main();

function parseIntOption(value) {
  const parsedValue = parseInt(value, 10);
  if (isNaN(parsedValue) || parsedValue < 0) throw new InvalidOptionArgumentError("Must be a non-negative integer.");

  return parsedValue;
}

function inc(x) {
  return x + 1;
}
