package main

import (
	"fmt"

	"danny.vn/vngcloud"
)

type pageFetcher[T any] func(page, size int) (*vngcloud.ListResult[T], error)

func record[T any](outputs *sdkOutputStore, client *vngcloud.Client, path, label string, items []T, err error) {
	outputs.add(path, client, items, err)
	printResult(label, len(items), err)
}

func recordGlobal[T any](outputs *sdkOutputStore, client *vngcloud.Client, path, label string, items []T, err error) {
	outputs.addGlobal(path, client, items, err)
	printResult(label, len(items), err)
}

func recordOne[T any](outputs *sdkOutputStore, client *vngcloud.Client, path, label string, item T, err error) {
	outputs.add(path, client, item, err)
	if err != nil {
		fmt.Printf("%s: error\n", label)
		return
	}
	fmt.Printf("%s: 1\n", label)
}

func printError(label string, err error) {
	fmt.Printf("  %s: %v\n", label, err)
}

func itemsOf[T any](result *vngcloud.ListResult[T]) []T {
	if result == nil {
		return nil
	}
	return result.Items
}

func collectPages[T any](size int, fetch pageFetcher[T]) ([]T, error) {
	first, err := fetch(1, size)
	if err != nil {
		return nil, err
	}
	if first == nil {
		return nil, nil
	}
	items := append([]T(nil), first.Items...)
	for page := 2; page <= first.Page.TotalPage; page++ {
		next, err := fetch(page, size)
		if err != nil {
			return nil, err
		}
		if next != nil {
			items = append(items, next.Items...)
		}
	}
	return items, nil
}

func collectDetails[T any, R any](items []T, id func(T) string, get func(string) (*R, error)) ([]*R, error) {
	details := make([]*R, 0, len(items))
	for _, item := range items {
		key := id(item)
		if key == "" {
			continue
		}
		detail, err := get(key)
		if err != nil {
			return details, err
		}
		details = append(details, detail)
	}
	return details, nil
}

func collectDetails2[T any, R any](items []T, ids func(T) (string, string), get func(string, string) (*R, error)) ([]*R, error) {
	details := make([]*R, 0, len(items))
	for _, item := range items {
		first, second := ids(item)
		if first == "" || second == "" {
			continue
		}
		detail, err := get(first, second)
		if err != nil {
			return details, err
		}
		details = append(details, detail)
	}
	return details, nil
}
