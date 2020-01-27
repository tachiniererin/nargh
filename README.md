# NARGH, a LCSC scraper

This scrapes LCSC, because i have ADD and can't finish the firmware for my boards.

That's all.

## Usage

Please use the pre-processed data. It's being updated semi-regularly and hosted [here](https://github.com/tachiniererin/nargh/releases).

To install it into `$GOPATH/bin` run:
```sh
go get github.com/tachiniererin/nargh
go install github.com/tachiniererin/nargh
```

Otherwise you can just clone the repo and run it with `go run nargh.go`.


## Product categories

Look for the string `分类数据` in the page source of [`https://lcsc.com/products`](https://lcsc.com/products).
This contains all the categories as a list. The file [`categories.json`](categories.json) contains
this data, just formatted nicely.

## Why is it called Nargh?

Because that was the sound I made when I realised what I did.

## TODO

- Product Pictures
- Datasheets
- Scrape JLCSMT part library (which was the initial goal)
- Make the terminal output a bit prettier.
